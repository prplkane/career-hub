package catalogtransaction

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/runtimepaths"
)

const testHomeID = "dh_0123456789abcdef0123456789abcdef"

type memory struct {
	files      map[string][]byte
	steps      int
	failAt     int
	writeCount int
	mutate     func(string)
}

func (m *memory) failure() *runtimepaths.Error {
	m.steps++
	if m.steps == m.failAt {
		return &runtimepaths.Error{Code: "SYNTHETIC_PRIVATE_ERROR"}
	}
	return nil
}
func (m *memory) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if e := m.failure(); e != nil {
		return nil, false, e
	}
	raw, present := m.files[path]
	if int64(len(raw)) > limit {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	return bytes.Clone(raw), present, nil
}
func (m *memory) RequireProcessOwnership() *runtimepaths.Error { return m.failure() }
func (m *memory) EnsureDirectory(string) (bool, *runtimepaths.Error) {
	e := m.failure()
	return e == nil, e
}
func (m *memory) publish(path string, raw []byte) (bool, *runtimepaths.Error) {
	if e := m.failure(); e != nil {
		return false, e
	}
	m.files[path] = bytes.Clone(raw)
	m.writeCount++
	if m.mutate != nil {
		m.mutate(path)
	}
	return true, m.failure()
}
func (m *memory) CreateFile(path string, raw []byte) (bool, *runtimepaths.Error) {
	if _, present := m.files[path]; present {
		return false, &runtimepaths.Error{Code: "FILE_EXISTS"}
	}
	return m.publish(path, raw)
}
func (m *memory) ReplaceFile(path string, before, after []byte) (bool, *runtimepaths.Error) {
	if raw, present := m.files[path]; !present || !bytes.Equal(raw, before) {
		return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
	}
	return m.publish(path, after)
}
func (m *memory) RemoveFile(path string, expected []byte) (bool, *runtimepaths.Error) {
	if e := m.failure(); e != nil {
		return false, e
	}
	if raw, present := m.files[path]; !present || !bytes.Equal(raw, expected) {
		return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
	}
	delete(m.files, path)
	return true, m.failure()
}
func fixture(missing bool) (*memory, []Change) {
	changes := []Change{
		{Path: catalogstore.CustomGroupsPath, BeforePresent: true, Before: []byte("groups before"), After: []byte("groups after")},
		{Path: catalogstore.CustomSkillsPath, BeforePresent: true, Before: []byte("skills before"), After: []byte("skills after")},
	}
	if missing {
		changes[1].BeforePresent = false
		changes[1].Before = nil
	}
	m := &memory{files: map[string][]byte{"documents/private.bin": []byte("unchanged")}}
	for _, c := range changes {
		if c.BeforePresent {
			m.files[c.Path] = bytes.Clone(c.Before)
		}
	}
	return m, changes
}
func accept(context.Context, []Change) error { return nil }
func assertAfter(t *testing.T, m *memory, changes []Change) {
	t.Helper()
	for _, change := range changes {
		if !bytes.Equal(m.files[change.Path], change.After) {
			t.Fatal("after state not reached")
		}
	}
	if _, present := m.files[JournalPath]; present {
		t.Fatal("completed transaction retains intent")
	}
	if string(m.files["documents/private.bin"]) != "unchanged" {
		t.Fatal("unrelated file changed")
	}
}

func TestEveryTransactionFailureAndRestartBoundary(t *testing.T) {
	for _, missing := range []bool{false, true} {
		baseline, changes := fixture(missing)
		r, err := begin(context.Background(), baseline, testHomeID, changes, accept)
		if err != nil || !r.Completed || !r.JournalRemoved || !r.FilesComplete || len(r.AppliedFiles) != 2 {
			t.Fatal("baseline publication failed", r, err)
		}
		assertAfter(t, baseline, changes)
		for point := 1; point <= baseline.steps; point++ {
			m, changes := fixture(missing)
			m.failAt = point
			r, err := begin(context.Background(), m, testHomeID, changes, accept)
			if err == nil {
				t.Fatalf("fault %d did not fail", point)
			}
			pending := m.files[JournalPath] != nil
			if pending && !r.IntentRecorded {
				t.Fatal("saved intent was hidden")
			}
			if len(r.AppliedFiles) > 0 && !r.IntentRecorded {
				t.Fatal("file changed without durable intent")
			}
			m.failAt = 0
			if pending {
				// A fresh invocation sees exactly the retained after/before mix.
				resumed, err := resume(context.Background(), m, testHomeID, accept)
				if err != nil || !resumed.Completed {
					t.Fatalf("fault %d recovery failed: %v %v", point, resumed, err)
				}
				assertAfter(t, m, changes)
			} else if r.JournalRemoved {
				assertAfter(t, m, changes)
			}
			again, err := resume(context.Background(), m, testHomeID, accept)
			if err != nil || again.IntentRecorded || len(again.AppliedFiles) != 0 {
				t.Fatal("recovery was not idempotent")
			}
		}
		t.Logf("verified %d begin failure/restart boundaries (missing=%v)", baseline.steps, missing)
	}
}

func TestRecoveryPreflightsEveryLeafAndRejectsUntrustedIntent(t *testing.T) {
	m, changes := fixture(false)
	raw, err := encode(testHomeID, changes)
	if err != nil {
		t.Fatal(err)
	}
	m.files[JournalPath] = raw
	m.files[changes[1].Path] = []byte("external edit")
	r, err := resume(context.Background(), m, testHomeID, accept)
	if err != ErrConflict || len(r.AppliedFiles) != 0 || m.writeCount != 0 {
		t.Fatal("recovery overwrote an unknown state")
	}
	if !bytes.Equal(m.files[changes[0].Path], changes[0].Before) {
		t.Fatal("first leaf changed before checking second")
	}
	for _, bad := range [][]byte{[]byte("{}"), append([]byte(" "), raw...), append(raw, []byte("null")...), bytes.Replace(raw, []byte("groups"), []byte("other!"), 1), []byte(`{"payload":{},"sha256":"bad","extra":true}`)} {
		m.files[JournalPath] = bad
		if _, err := resume(context.Background(), m, testHomeID, accept); err != ErrJournal {
			t.Fatal("invalid journal admitted")
		}
	}
	m.files[JournalPath] = raw
	if _, err := resume(context.Background(), m, "dh_22222222222222222222222222222222", accept); err != ErrJournal {
		t.Fatal("foreign-home intent accepted")
	}
}

func TestTransactionValidationCancellationAndPublicationFacts(t *testing.T) {
	for _, mode := range []string{"validation", "mutating-validator", "canceled", "cancel-after-first", "cancel-after-last", "change-after-validation"} {
		m, changes := fixture(false)
		ctx, cancel := context.WithCancel(context.Background())
		validate := Validate(accept)
		switch mode {
		case "validation":
			validate = func(context.Context, []Change) error { return errors.New("invalid target") }
		case "mutating-validator":
			validate = func(_ context.Context, c []Change) error { c[0].After = []byte("not in intent"); return nil }
		case "change-after-validation":
			validate = func(context.Context, []Change) error { m.files[changes[1].Path] = []byte("external"); return nil }
		case "canceled":
			cancel()
		case "cancel-after-first":
			m.mutate = func(path string) {
				if path == changes[0].Path {
					cancel()
				}
			}
		case "cancel-after-last":
			m.mutate = func(path string) {
				if path == changes[1].Path {
					cancel()
				}
			}
		}
		r, err := begin(ctx, m, testHomeID, changes, validate)
		cancel()
		if mode == "cancel-after-last" {
			if err != nil || !r.Completed {
				t.Fatal("late cancellation hid completion")
			}
			continue
		}
		if err == nil {
			t.Fatal("invalid/canceled transaction succeeded")
		}
		if mode == "cancel-after-first" {
			if !r.IntentRecorded || len(r.AppliedFiles) != 1 || r.Completed {
				t.Fatal("partial facts lost")
			}
		} else if r.IntentRecorded || m.files[JournalPath] != nil {
			t.Fatal("invalid operation recorded intent")
		}
	}
	for _, c := range [][]Change{nil, {{Path: "../outside"}}, {{Path: catalogstore.CustomGroupsPath, Before: []byte("unexpected")}}} {
		if _, err := encode(testHomeID, c); err == nil {
			t.Fatal("unsafe plan encoded")
		}
	}
	if canonical(bytes.Repeat([]byte("["), 17), new(any)) {
		t.Fatal("deep journal accepted")
	}
	if canonical([]byte{0xff}, new(any)) {
		t.Fatal("invalid UTF8 accepted")
	}
	if canonical([]byte(`{"version":1,"version":1}`), new(intent)) {
		t.Fatal("duplicate journal field accepted")
	}
	if _, err := encode(testHomeID, []Change{{Path: catalogstore.CustomGroupsPath, After: bytes.Repeat([]byte("x"), 3<<20)}}); err == nil {
		t.Fatal("oversized catalog encoded")
	}
}
