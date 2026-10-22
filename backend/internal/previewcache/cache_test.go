package previewcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/discoverypreview"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobevaluation"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/skills/catalog"
)

func cacheFixture(t *testing.T) (assessmentcontext.CacheIdentity, *discoverypreview.Prepared) {
	t.Helper()
	registry, e := catalog.Build(nil, nil, []catalog.Term{{ID: "python", Name: "Python", Category: "programming_language"}}, nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	source := "Company: Synthetic\nRole: Engineer\nRequirements\nPython experience.\n"
	hash := sha256.Sum256([]byte(source))
	profile, e2 := cataloginput.ParseYAML([]byte("current_status: citizen\nrequires_sponsorship_now: false\n"))
	if e2 != nil {
		t.Fatal(e2)
	}
	prepared, err := discoverypreview.Build(context.Background(), discoverypreview.Input{
		Capture:  discoverypreview.Capture{Identity: assessmentcontext.CacheCapture{DiscoveryID: "synthetic", Scope: "confirmed_full", ContentSHA256: hex.EncodeToString(hash[:]), Title: "Engineer", Company: "Synthetic", ObservedURL: "https://example.test/job/1"}, Source: source, RelativePath: "jobs/discovery/captures/synthetic.txt"},
		Registry: registry, Evaluation: jobevaluation.Input{Profile: profile, Policy: cataloginput.Value{Kind: "mapping"}},
		CheckSource: func(context.Context, string) (bool, *jobcard.Error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return assessmentcontext.CacheIdentity{Version: 2, Candidate: "ch-candidate-v2:sha256:" + strings.Repeat("a", 64), Context: "ch-context-v2:sha256:" + strings.Repeat("b", 64), Key: "ch-assessment-v2:sha256:" + strings.Repeat("c", 64)}, prepared
}
func fixtureTime() time.Time { return time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC) }
func TestCanonicalEnvelopeAndDisposition(t *testing.T) {
	identity, prepared := cacheFixture(t)
	raw, err := Encode(context.Background(), "synthetic", identity, prepared, fixtureTime())
	if err != nil {
		t.Fatal(err)
	}
	got, state, err := Decode(context.Background(), raw, "synthetic", identity)
	if err != nil || state != Match || got == nil {
		t.Fatalf("roundtrip failed: %s %v", state, err)
	}
	if next, err := Encode(context.Background(), "synthetic", identity, got, fixtureTime()); err != nil || !bytes.Equal(raw, next) {
		t.Fatal("roundtrip changed bytes")
	}
	for _, mode := range []string{"checksum", "payload", "unknown", "duplicate", "missing", "whitespace", "trailing", "depth", "utf8", "oversize", "schema", "version", "identity"} {
		t.Run(mode, func(t *testing.T) {
			changed := bytes.Clone(raw)
			switch mode {
			case "checksum":
				changed = bytes.Replace(changed, []byte(`"sha256":"`), []byte(`"sha256":"0`), 1)
			case "payload":
				changed = bytes.Replace(changed, []byte("Engineer"), []byte("Manager"), 1)
			case "unknown":
				changed = append([]byte(`{"unknown":1,`), changed[1:]...)
			case "duplicate":
				changed = append([]byte(`{"sha256":"x",`), changed[1:]...)
			case "missing":
				changed = []byte(`{}`)
			case "whitespace":
				changed = append(changed, '\n')
			case "trailing":
				changed = append(changed, []byte(`{}`)...)
			case "depth":
				changed = []byte(strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65))
			case "utf8":
				changed = []byte{255}
			case "oversize":
				changed = make([]byte, MaxBytes+1)
			default:
				var wrapped envelope
				_ = json.Unmarshal(changed, &wrapped)
				var data payload
				decoder := json.NewDecoder(bytes.NewReader(wrapped.Payload))
				decoder.UseNumber()
				_ = decoder.Decode(&data)
				switch mode {
				case "schema":
					data.Artifact.Receipt.SchemaVersion = 999
				case "version":
					data.Versions[0]++
				case "identity":
					data.Identity.Key = "sha256:" + strings.Repeat("a", 64)
				}
				wrapped.Payload, _ = json.Marshal(data)
				sum := sha256.Sum256(wrapped.Payload)
				wrapped.SHA256 = hex.EncodeToString(sum[:])
				changed, _ = json.Marshal(wrapped)
			}
			got, state, err := Decode(context.Background(), changed, "synthetic", identity)
			if err != nil || got != nil || state == Match {
				t.Fatalf("bad cache accepted: %s %v", state, err)
			}
		})
	}
	if _, state, err := Decode(context.Background(), raw, "another", identity); err != nil || state != Stale {
		t.Fatal("cross-job reuse")
	}
	for _, id := range []string{"", "../escape", " a ", "C:drive", "a/b"} {
		if _, err := Path(id); err == nil {
			t.Fatal("unsafe identity accepted")
		}
	}
}

type fakeStore struct {
	files     map[string][]byte
	step      int
	failAt    int
	writeMode string
	writes    int
	cancel    context.CancelFunc
}

func (f *fakeStore) fault() *runtimepaths.Error {
	f.step++
	if f.step == f.failAt {
		return &runtimepaths.Error{Code: "SYNTHETIC_PRIVATE_FAILURE"}
	}
	return nil
}
func (f *fakeStore) ReadFile(path string, _ int64) ([]byte, bool, *runtimepaths.Error) {
	if e := f.fault(); e != nil {
		return nil, false, e
	}
	raw, present := f.files[path]
	return bytes.Clone(raw), present, nil
}
func (f *fakeStore) RequireProcessOwnership() *runtimepaths.Error { return f.fault() }
func (f *fakeStore) EnsureDirectory(string) (bool, *runtimepaths.Error) {
	e := f.fault()
	return e == nil, e
}
func (f *fakeStore) write(path string, raw []byte) (bool, *runtimepaths.Error) {
	f.writes++
	if e := f.fault(); e != nil {
		return false, e
	}
	f.files[path] = bytes.Clone(raw)
	if f.cancel != nil {
		f.cancel()
	}
	if f.writeMode == "postcommit" {
		return true, &runtimepaths.Error{Code: "SYNTHETIC_PRIVATE_FAILURE"}
	}
	return true, nil
}
func (f *fakeStore) CreateFile(path string, raw []byte) (bool, *runtimepaths.Error) {
	if _, exists := f.files[path]; exists {
		return false, &runtimepaths.Error{Code: "FILE_EXISTS"}
	}
	return f.write(path, raw)
}
func (f *fakeStore) ReplaceFile(path string, old, raw []byte) (bool, *runtimepaths.Error) {
	if !bytes.Equal(f.files[path], old) {
		return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
	}
	return f.write(path, raw)
}
func TestPublicationFailureFactsAndCAS(t *testing.T) {
	identity, prepared := cacheFixture(t)
	path, _ := Path("synthetic")
	for _, existing := range []bool{false, true} {
		for _, mode := range []string{"success", "postcommit", "canceled-after", "stale"} {
			store := &fakeStore{files: map[string][]byte{}}
			if existing {
				store.files[path] = []byte("old invalid cache")
			}
			entry, err := Load(context.Background(), store, "synthetic", identity)
			if err != nil {
				t.Fatal(err)
			}
			store.step = 0
			store.writeMode = mode
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "canceled-after" {
				store.cancel = cancel
			}
			if mode == "stale" {
				store.files[path] = []byte("external edit")
			}
			report, err := save(ctx, store, entry.Observation, "synthetic", identity, prepared, fixtureTime())
			cancel()
			if mode == "stale" {
				if report.Committed || report.Attempted || err != ErrConflict || store.writes != 0 {
					t.Fatal("stale observation overwrote file")
				}
				continue
			}
			if !report.Committed || !report.Attempted || store.writes != 1 {
				t.Fatal("lost publication facts")
			}
			if (err != nil) != (mode == "postcommit") {
				t.Fatalf("incorrect error outcome %s: %v", mode, err)
			}
			if err != nil && strings.Contains(err.Error(), "SYNTHETIC_PRIVATE_FAILURE") {
				t.Fatal("private error leaked")
			}
		}
	}
	for _, existing := range []bool{false, true} {
		files := func() map[string][]byte {
			result := map[string][]byte{}
			if existing {
				result[path] = []byte("old cache")
			}
			return result
		}
		baseline := &fakeStore{files: files()}
		entry, err := Load(context.Background(), baseline, "synthetic", identity)
		if err != nil {
			t.Fatal(err)
		}
		baseline.step = 0
		if _, err := save(context.Background(), baseline, entry.Observation, "synthetic", identity, prepared, fixtureTime()); err != nil {
			t.Fatal(err)
		}
		for point := 1; point <= baseline.step; point++ {
			store := &fakeStore{files: files(), failAt: point}
			report, err := save(context.Background(), store, entry.Observation, "synthetic", identity, prepared, fixtureTime())
			if err == nil {
				t.Fatalf("failure point %d ignored", point)
			}
			raw, exists := store.files[path]
			published := exists && !bytes.Equal(raw, []byte("old cache"))
			if report.Committed != published {
				t.Fatal("error hid publication")
			}
		}
	}
}

func TestCacheCancellationAndInvalidBoundaries(t *testing.T) {
	identity, prepared := cacheFixture(t)
	for _, ctx := range []context.Context{nil, func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }()} {
		store := &fakeStore{files: map[string][]byte{}}
		if got, err := Load(ctx, store, "synthetic", identity); got != nil || err == nil || store.step != 0 {
			t.Fatal("canceled read performed IO")
		}
		if raw, err := Encode(ctx, "synthetic", identity, prepared, fixtureTime()); raw != nil || err == nil {
			t.Fatal("canceled encode returned artifact")
		}
		if got, _, err := Decode(ctx, []byte("{}"), "synthetic", identity); got != nil || err == nil {
			t.Fatal("canceled decode returned artifact")
		}
	}
	if got, err := Load(context.Background(), nil, "synthetic", identity); got != nil || err == nil {
		t.Fatal("nil reader admitted")
	}
	path, _ := Path("synthetic")
	for _, store := range []*fakeStore{{files: map[string][]byte{}, failAt: 1}, {files: map[string][]byte{path: bytes.Repeat([]byte("x"), MaxBytes+1)}}} {
		if got, err := Load(context.Background(), store, "synthetic", identity); got != nil || err != ErrRead {
			t.Fatal("failed/oversized read treated as a cache miss")
		}
	}
	for _, invalid := range []*discoverypreview.Prepared{nil, {}, {Artifact: prepared.Artifact}} {
		if raw, err := Encode(context.Background(), "synthetic", identity, invalid, fixtureTime()); raw != nil || err == nil {
			t.Fatal("partial cache encoded")
		}
	}
}
