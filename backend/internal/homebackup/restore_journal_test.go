package homebackup

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/datahome"
)

func journalFixture(t *testing.T) (restoreJournal, restoreEvidence) {
	t.Helper()
	marker, markerProblem := datahome.MarkerBytes("stable", testHomeID)
	if markerProblem != nil {
		t.Fatal(markerProblem)
	}
	j, e, p := makeRestoreJournal(strings.Repeat("a", 32), strings.Repeat("b", 64), marker,
		testManifest(testFile("jobs/original.txt", "original")), testManifest(testFile("jobs/new.txt", "new")), map[string]bool{"jobs": true})
	if p != nil {
		t.Fatal(p)
	}
	return j, e
}

func TestRestoreJournalCanonicalRoundTrip(t *testing.T) {
	j, e := journalFixture(t)
	raw, p := encodeRestoreJournal(j, j.RootFingerprint)
	if p != nil {
		t.Fatal(p)
	}
	got, p := decodeRestoreJournal(raw, j.RootFingerprint)
	if p != nil || !reflect.DeepEqual(got, j) || validateRestoreEvidence(got, e) != nil {
		t.Fatal(got, p)
	}
	if j.recoveryPath() != "backups/restore-recovery/restore_"+j.RestoreID || j.stagePath() != restorePrefix+j.RestoreID {
		t.Fatal("unconfined derived paths")
	}
	for _, fingerprint := range []string{"", strings.Repeat("c", 64), strings.Repeat("B", 64)} {
		if _, p := decodeRestoreJournal(raw, fingerprint); p == nil || p.Code != "BACKUP_RESTORE_SCOPE_MISMATCH" {
			t.Fatal("scope accepted", p)
		}
	}
	invalid := [][]byte{nil, []byte("{}"), []byte(`{"version":1,"restore_id":"legacy"}`), append(bytes.Clone(raw), '\n'),
		bytes.Replace(raw, []byte(`"version":2`), []byte(`"version":2,"version":2`), 1),
		bytes.Replace(raw, []byte(`"version":2`), []byte(`"version":2.0`), 1),
		bytes.Replace(raw, []byte(`"had_active":true`), []byte(`"had_active":false`), 1),
		[]byte(strings.Repeat(" ", maxRestoreJournalBytes+1)), append(bytes.Clone(raw), []byte("{}")...)}
	for n, raw := range invalid {
		if got, p := decodeRestoreJournal(raw, j.RootFingerprint); p == nil || !reflect.DeepEqual(got, restoreJournal{}) {
			t.Fatalf("invalid input %d accepted", n)
		}
	}
}

func TestRestoreJournalShapeRefusals(t *testing.T) {
	changes := []func(*restoreJournal){
		func(j *restoreJournal) { j.Version = 1 }, func(j *restoreJournal) { j.RestoreID = "../outside" },
		func(j *restoreJournal) { j.BeforeHome.Initialized = false }, func(j *restoreJournal) { j.AfterHome.Channel = "dev" },
		func(j *restoreJournal) { j.AfterHome.DataHomeID = "dh_" + strings.Repeat("f", 32) },
		func(j *restoreJournal) { j.BeforeMarkerSHA256 = "" }, func(j *restoreJournal) { j.AfterMarkerSHA256 = strings.Repeat("a", 64) },
		func(j *restoreJournal) { j.Roots = nil }, func(j *restoreJournal) { j.Roots = []restoreRoot{{"jobs", true}, {"jobs", false}} },
		func(j *restoreJournal) { j.Roots = []restoreRoot{{"jobs", true}, {"documents", false}} },
		func(j *restoreJournal) { j.Roots = []restoreRoot{{"secrets", false}} },
	}
	for n, change := range changes {
		j, _ := journalFixture(t)
		change(&j)
		if _, p := encodeRestoreJournal(j, j.RootFingerprint); p == nil {
			t.Fatalf("invalid shape %d accepted", n)
		}
		payload, _ := json.Marshal(j)
		raw, _ := json.Marshal(restoreEnvelope{payload, restoreDigest(payload)})
		if _, p := decodeRestoreJournal(raw, j.RootFingerprint); p == nil {
			t.Fatalf("checksummed invalid shape %d accepted", n)
		}
	}
}

func TestRestoreJournalIdentityAndEvidence(t *testing.T) {
	marker, _ := datahome.MarkerBytes("stable", testHomeID)
	// Preserve a valid noncanonical marker byte-for-byte unless identity changes.
	marker = append([]byte("  "), marker...)
	for _, documents := range []bool{false, true} {
		after := testManifest(testFile("jobs/new.txt", "new"))
		active := map[string]bool{"jobs": false}
		if documents {
			after.Files = append(after.Files, testFile("documents/key_state.json", "fixture"))
			active["documents"] = false
		}
		after.DataHomeID = "dh_" + strings.Repeat("f", 32)
		j, e, p := makeRestoreJournal(strings.Repeat("a", 32), strings.Repeat("b", 64), marker, testManifest(), after, active)
		if p != nil || validateRestoreEvidence(j, e) != nil {
			t.Fatal(p)
		}
		if documents {
			if j.AfterHome.DataHomeID != after.DataHomeID || bytes.Equal(e.beforeMarker, e.afterMarker) {
				t.Fatal("document identity not adopted")
			}
		} else if j.AfterHome != j.BeforeHome || !bytes.Equal(e.afterMarker, marker) {
			t.Fatal("unselected document identity changed")
		}
		for _, field := range []string{"before-marker", "after-marker", "before-manifest", "after-manifest"} {
			bad := e
			switch field {
			case "before-marker":
				bad.beforeMarker = []byte("{}")
			case "after-marker":
				bad.afterMarker = []byte("{}")
			case "before-manifest":
				bad.beforeManifest = []byte("{}")
			case "after-manifest":
				bad.afterManifest = []byte("{}")
			}
			if validateRestoreEvidence(j, bad) == nil {
				t.Fatal("bad evidence accepted", field)
			}
		}
	}
	for _, active := range []map[string]bool{nil, {"jobs": true, "logs": false}, {"logs": true}} {
		if _, _, p := makeRestoreJournal(strings.Repeat("a", 32), strings.Repeat("b", 64), marker, testManifest(), testManifest(testFile("jobs/a.txt", "a")), active); p == nil {
			t.Fatal("root mismatch accepted")
		}
	}
	wrongChannel := testManifest()
	wrongChannel.DataHomeChannel = "dev"
	if _, _, p := makeRestoreJournal(strings.Repeat("a", 32), strings.Repeat("b", 64), marker, testManifest(), wrongChannel, map[string]bool{}); p == nil {
		t.Fatal("cross-channel archive accepted")
	}
	j, e, p := makeRestoreJournal(strings.Repeat("a", 32), strings.Repeat("b", 64), marker, testManifest(), testManifest(), map[string]bool{})
	if p != nil || len(j.Roots) != 0 || validateRestoreEvidence(j, e) != nil {
		t.Fatal("empty archive contract", p)
	}
}

type boundedRestoreReader struct {
	raw   []byte
	calls int
	short bool
}

func (r *boundedRestoreReader) ReadAt(out []byte, offset int64) (int, error) {
	if len(out) > 64<<10 {
		return 0, io.ErrShortBuffer
	}
	r.calls++
	if r.short {
		return 0, io.EOF
	}
	n := copy(out, r.raw[offset:])
	return n, nil
}

func TestRestoreEvidenceReadBound(t *testing.T) {
	r := &boundedRestoreReader{raw: bytes.Repeat([]byte("a"), 700000)}
	got, err := readRestoreEvidenceBytes(r, int64(len(r.raw)))
	if err != nil || !bytes.Equal(got, r.raw) || r.calls < 10 {
		t.Fatal("bounded read failed", err)
	}
	r.short = true
	if got, err := readRestoreEvidenceBytes(r, 1); err == nil || got != nil {
		t.Fatal("short read accepted")
	}
	for _, size := range []int64{-1, MaxManifestBytes + 1} {
		if _, err := readRestoreEvidenceBytes(r, size); err == nil {
			t.Fatal("invalid size accepted")
		}
	}
}
