package applicationwrite

import (
	"bytes"
	"strings"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/jobdigest"
)

func testNewJobIntent(raw, card []byte) newJobIntent {
	return newJobIntent{
		Version: 1, Home: datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true,
			DataHomeID: "dh_" + strings.Repeat("a", 32)},
		OperationID: strings.Repeat("b", 32), InitialStatus: "captured", JobID: "software_engineer",
		RawPath: "jobs/incoming/software_engineer.md", CardPath: "jobs/normalized/software_engineer.yaml",
		RawSize: len(raw), RawSHA256: digestBytes(raw), CardSize: len(card), CardSHA256: digestBytes(card),
		ContentFingerprint: strings.Repeat("c", 64),
	}
}

func TestNewJobIntentCodec(t *testing.T) {
	raw, card := []byte("private fictional JD"), []byte("id: software_engineer\n")
	p := testNewJobIntent(raw, card)
	encoded, err := encodeNewJobIntent(p)
	if err != nil || bytes.Contains(encoded, raw) || len(encoded) > newJobIntentLimit {
		t.Fatal("intent must be bounded, metadata-only, and encodable")
	}
	decoded, err := decodeNewJobIntent(encoded, p.Home)
	if err != nil || decoded != p {
		t.Fatal("canonical intent did not roundtrip")
	}
	p.InitialStatus = "applied"
	applied, err := encodeNewJobIntent(p)
	if err != nil {
		t.Fatal("explicit Applied intent was refused")
	}
	if decoded, err := decodeNewJobIntent(applied, p.Home); err != nil || decoded.InitialStatus != "applied" {
		t.Fatal("explicit Applied intent did not roundtrip")
	}
	otherHome := p.Home
	otherHome.DataHomeID = "dh_" + strings.Repeat("d", 32)
	if _, err := decodeNewJobIntent(encoded, otherHome); err == nil || err.Code != "JOB_INTAKE_INTENT_HOME_CHANGED" {
		t.Fatal("foreign Home was admitted")
	}
	for name, mutated := range map[string][]byte{
		"checksum": bytes.Replace(encoded, []byte(`"sha256":"`), []byte(`"sha256":"0`), 1),
		"extra":    append(bytes.Clone(encoded), ' '),
		"limit":    bytes.Repeat([]byte("x"), newJobIntentLimit+1),
	} {
		if _, err := decodeNewJobIntent(mutated, p.Home); err == nil || err.Code != "JOB_INTAKE_INTENT_INVALID" {
			t.Errorf("%s: malformed intent was admitted", name)
		}
	}
}

func TestNewJobIntentRefusesInvalidBinding(t *testing.T) {
	p := testNewJobIntent([]byte("JD"), []byte("card"))
	for name, change := range map[string]func(*newJobIntent){
		"missing-intent": func(v *newJobIntent) { v.InitialStatus = "" },
		"bad-intent":     func(v *newJobIntent) { v.InitialStatus = "interview" },
		"foreign-raw":    func(v *newJobIntent) { v.RawPath = "jobs/incoming/other.md" },
		"foreign-card":   func(v *newJobIntent) { v.CardPath = "jobs/normalized/other.yaml" },
		"reserved-name":  func(v *newJobIntent) { v.JobID = "CON" },
		"unsafe-name":    func(v *newJobIntent) { v.JobID = "bad name" },
		"wrong-home":     func(v *newJobIntent) { v.Home.Initialized = false },
		"bad-size":       func(v *newJobIntent) { v.RawSize = jobdigest.MaxSourceBytes + 1 },
		"bad-hash":       func(v *newJobIntent) { v.CardSHA256 = "not-a-digest" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := p
			change(&changed)
			if _, err := encodeNewJobIntent(changed); err == nil || err.Code != "JOB_INTAKE_INTENT_INVALID" {
				t.Fatal("invalid identity was encoded")
			}
		})
	}
}

func TestNewJobFileObservationNeverInfersRollback(t *testing.T) {
	raw, card := []byte("private fictional JD"), []byte("id: software_engineer\n")
	p := testNewJobIntent(raw, card)
	exactRaw := newJobObservedFile{Present: true, Bytes: raw}
	exactCard := newJobObservedFile{Present: true, Bytes: card}
	for name, tc := range map[string]struct {
		raw, card newJobObservedFile
		want      newJobFileState
	}{
		"neither":       {want: newJobFilesNone},
		"raw-only":      {raw: exactRaw, want: newJobFilesRawOnly},
		"both":          {raw: exactRaw, card: exactCard, want: newJobFilesComplete},
		"card-only":     {card: exactCard, want: newJobFilesConflict},
		"wrong-raw":     {raw: newJobObservedFile{Present: true, Bytes: []byte("other")}, want: newJobFilesConflict},
		"wrong-card":    {raw: exactRaw, card: newJobObservedFile{Present: true, Bytes: []byte("other")}, want: newJobFilesConflict},
		"missing-bytes": {raw: newJobObservedFile{Bytes: raw}, want: newJobFilesConflict},
	} {
		t.Run(name, func(t *testing.T) {
			if got := classifyNewJobFiles(p, tc.raw, tc.card); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
