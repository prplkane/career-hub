package catalogtransaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func approvalFixture(missing, checks bool) (*memory, intent) {
	m, catalogs := fixture(missing)
	data := intent{Version: 3, HomeID: testHomeID}
	for _, path := range approvalVaultPaths {
		before, after := []byte("before "+path), []byte("after "+path)
		m.files[path] = bytes.Clone(before)
		data.Changes = append(data.Changes, Change{Path: path, BeforePresent: true, Before: before, After: after})
	}
	data.Changes = append(data.Changes, catalogs...)
	if checks {
		for i := range data.Changes {
			data.Changes[i].CheckOnly = true
			data.Changes[i].After = bytes.Clone(data.Changes[i].Before)
		}
	}
	sourceID := "src_fictional_1234567890"
	base := "career_vault/intake_runs/" + sourceID + "/"
	data.Changes = append(data.Changes, Change{Path: base + "approval_result.yaml", RecordID: sourceID, After: []byte("verified domain result")})
	for _, leaf := range []string{"extracted.yaml", "review.yaml"} {
		raw := []byte("source " + leaf)
		m.files[base+leaf] = raw
		data.Guards = append(data.Guards, Guard{Path: base + leaf, Revision: fmt.Sprintf("sha256:%x", sha256.Sum256(raw))})
	}
	return m, data
}

func assertApprovalAfter(t *testing.T, m *memory, data intent) {
	t.Helper()
	for _, c := range data.Changes {
		if _, after, err := observe(m, c); err != nil || !after {
			t.Fatal("incomplete after-state")
		}
	}
	if _, present := m.files[ApprovalJournalPath]; present {
		t.Fatal("completed journal retained")
	}
	if err := checkGuards(context.Background(), m, data.Guards); err != nil {
		t.Fatal("source changed")
	}
	if string(m.files["documents/private.bin"]) != "unchanged" {
		t.Fatal("unrelated data changed")
	}
}

func TestApprovalEveryFailureAndRestartBoundary(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, checks := range []bool{false, true} {
			baseline, data := approvalFixture(missing, checks)
			r, err := beginIntent(context.Background(), baseline, data, accept)
			wantWrites := 8
			if checks {
				wantWrites = 1
			}
			if err != nil || !r.Completed || len(r.AppliedFiles) != wantWrites {
				t.Fatal("baseline", err)
			}
			for point := 1; point <= baseline.steps; point++ {
				m, data := approvalFixture(missing, checks)
				m.failAt = point
				r, err := beginIntent(context.Background(), m, data, accept)
				if err == nil {
					t.Fatalf("failure %d not exercised", point)
				}
				_, pending := m.files[ApprovalJournalPath]
				if pending && !r.IntentRecorded || len(r.AppliedFiles) > 0 && !r.IntentRecorded {
					t.Fatal("lost commit facts")
				}
				m.failAt = 0
				if pending {
					r, err = resumePolicy(context.Background(), m, testHomeID, 3, accept)
					if err != nil || !r.Completed {
						t.Fatalf("restart %d: %v", point, err)
					}
					assertApprovalAfter(t, m, data)
				} else if r.JournalRemoved {
					assertApprovalAfter(t, m, data)
				} else {
					for _, c := range data.Changes {
						if before, _, err := observe(m, c); err != nil || !before {
							t.Fatal("unrecorded mutation")
						}
					}
				}
				again, err := resumePolicy(context.Background(), m, testHomeID, 3, accept)
				if err != nil || again.IntentRecorded || len(again.AppliedFiles) > 0 {
					t.Fatal("non-idempotent recovery")
				}
			}
			t.Logf("%d boundaries; missing catalog=%v, check-only=%v", baseline.steps, missing, checks)
		}
	}
}

func TestApprovalClosedPolicyAndVersionIsolation(t *testing.T) {
	for i, alter := range []func(*intent){
		func(d *intent) { d.Version = 1 }, func(d *intent) { d.Version = 2 }, func(d *intent) { d.HomeID = "wrong" },
		func(d *intent) { d.Changes = d.Changes[:7] }, func(d *intent) { d.Changes[0], d.Changes[1] = d.Changes[1], d.Changes[0] },
		func(d *intent) { d.Changes[0].Path = "documents/private.bin" }, func(d *intent) { d.Changes[0].BeforePresent = false },
		func(d *intent) { d.Changes[0].RecordID = "record" }, func(d *intent) { d.Changes[0].CheckOnly = true },
		func(d *intent) { d.Changes[0].After = make([]byte, cataloginput.YAMLMaxInputBytes+1) },
		func(d *intent) { d.Changes[5].Path = d.Changes[6].Path }, func(d *intent) { d.Changes[5].BeforePresent = false },
		func(d *intent) { d.Changes[6].RecordID = "record" }, func(d *intent) { d.Changes[5].CheckOnly = true; d.Changes[6].CheckOnly = true },
		func(d *intent) { d.Changes[7].Path = "career_vault/intake_runs/src_other/approval_result.yaml" },
		func(d *intent) { d.Changes[7].RecordID = "src_../outside" }, func(d *intent) { d.Changes[7].BeforePresent = true },
		func(d *intent) { d.Changes[7].Before = []byte("old result") }, func(d *intent) { d.Changes[7].After = nil },
		func(d *intent) { d.Changes[7].After = make([]byte, cataloginput.YAMLMaxInputBytes+1) }, func(d *intent) { d.Changes[7].CheckOnly = true },
		func(d *intent) { d.Guards = d.Guards[:1] }, func(d *intent) { d.Guards[0] = d.Guards[1] },
		func(d *intent) { d.Guards[0].Path = "documents/private.bin" }, func(d *intent) { d.Guards[0].Revision = "sha256:invalid" },
	} {
		m, data := approvalFixture(false, false)
		alter(&data)
		if _, err := encodeIntent(data); !errors.Is(err, ErrInput) {
			t.Fatalf("policy %d accepted", i)
		}
		m.files[ApprovalJournalPath] = resign(data)
		if _, err := resumePolicy(context.Background(), m, testHomeID, 3, accept); !errors.Is(err, ErrJournal) || m.writeCount != 0 {
			t.Fatalf("forged policy %d replayed", i)
		}
	}
	_, data := approvalFixture(false, false)
	raw, err := encodeIntent(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		if _, err := decodeIntent(raw, testHomeID, version); !errors.Is(err, ErrJournal) {
			t.Fatal("approval accepted as another policy")
		}
	}
	if _, err := decodeIntent(raw, "dh_22222222222222222222222222222222", 3); !errors.Is(err, ErrJournal) {
		t.Fatal("foreign Home accepted")
	}
	for _, ctx := range []context.Context{nil, context.Background()} {
		if _, err := BeginApprovalOwned(ctx, nil, testHomeID, data.Changes, data.Guards, accept); err == nil {
			t.Fatal("unowned begin")
		}
		if _, err := RecoverApprovalOwned(ctx, nil, testHomeID, accept); err == nil {
			t.Fatal("unowned recovery")
		}
	}
}

func TestApprovalAggregateJournalLimitRefusesBeforePublication(t *testing.T) {
	m, data := approvalFixture(false, false)
	for i := range data.Changes[:7] {
		data.Changes[i].Before = bytes.Repeat([]byte("b"), cataloginput.YAMLMaxInputBytes)
		data.Changes[i].After = bytes.Repeat([]byte("a"), cataloginput.YAMLMaxInputBytes)
		m.files[data.Changes[i].Path] = bytes.Clone(data.Changes[i].Before)
	}
	if !validApprovalIntent(data) {
		t.Fatal("fixture exceeds a leaf bound")
	}
	if report, err := beginIntent(context.Background(), m, data, accept); !errors.Is(err, ErrInput) || report.IntentRecorded || m.writeCount != 0 {
		t.Fatal("aggregate oversize reached publication")
	}
}

func TestApprovalSourceDriftAndThirdStatesNeverOverwrite(t *testing.T) {
	for _, guardIndex := range []int{0, 1} {
		for point := -2; point < 8; point++ {
			m, data := approvalFixture(false, false)
			path := data.Guards[guardIndex].Path
			change := func() { m.files[path] = []byte("external source edit") }
			validate := accept
			if point == -2 {
				validate = func(context.Context, []Change) error { change(); return nil }
			} else {
				at := ApprovalJournalPath
				if point >= 0 {
					at = data.Changes[point].Path
				}
				m.mutate = func(path string) {
					if path == at {
						change()
					}
				}
			}
			r, err := beginIntent(context.Background(), m, data, validate)
			if !errors.Is(err, ErrConflict) || r.Completed || r.JournalRemoved {
				t.Fatalf("source drift accepted at %d: %v", point, err)
			}
			writes := m.writeCount
			m.mutate = nil
			if r.IntentRecorded {
				if _, err := resumePolicy(context.Background(), m, testHomeID, 3, accept); !errors.Is(err, ErrConflict) || m.writeCount != writes {
					t.Fatal("changed source replayed")
				}
			}
		}
	}
	for index := 0; index < 8; index++ {
		m, data := approvalFixture(false, false)
		raw, _ := encodeIntent(data)
		m.files[ApprovalJournalPath] = raw
		m.files[data.Changes[index].Path] = []byte("external change")
		if _, err := resumePolicy(context.Background(), m, testHomeID, 3, accept); !errors.Is(err, ErrConflict) || m.writeCount != 0 {
			t.Fatal("third state replayed before complete preflight")
		}
	}
}

type ambiguousApproval struct {
	*memory
	at string
}

func (m *ambiguousApproval) CreateFile(path string, raw []byte) (bool, *runtimepaths.Error) {
	committed, err := m.memory.CreateFile(path, raw)
	if path == m.at && committed {
		return false, &runtimepaths.Error{Code: "SYNTHETIC_UNKNOWN_OUTCOME"}
	}
	return committed, err
}
func TestApprovalAmbiguousCreateRetainsRecoveryEvidence(t *testing.T) {
	for _, result := range []bool{false, true} {
		m, data := approvalFixture(false, false)
		at := ApprovalJournalPath
		if result {
			at = data.Changes[7].Path
		}
		r, err := beginIntent(context.Background(), &ambiguousApproval{m, at}, data, accept)
		if !errors.Is(err, ErrIO) || r.Completed || r.JournalRemoved {
			t.Fatal("uncertain create claimed success")
		}
		if _, present := m.files[ApprovalJournalPath]; !present {
			t.Fatal("uncertain intent removed")
		}
		r, err = resumePolicy(context.Background(), m, testHomeID, 3, accept)
		if err != nil || !r.Completed {
			t.Fatal("uncertain create not recoverable", err)
		}
		assertApprovalAfter(t, m, data)
	}
}
