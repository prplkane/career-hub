package applicationwrite

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
)

func testResumeIntent() resumeIntent {
	const id = "resume_00000000000000000000000000000001"
	return resumeIntent{
		Version: 1, Operation: "upload",
		Home:   datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"},
		File:   resumelibrary.FileChange{ResumeID: id, RelativePath: resumelibrary.LibraryPath + "/" + id + ".pdf", AfterPresent: true, After: []byte{0, 255, 'x'}},
		Before: resumelibrary.Witness{ResumeID: id},
		After:  resumelibrary.Witness{ResumeID: id, Present: true, SHA256: digestBytes([]byte("synthetic exact-row witness"))},
	}
}

func resumeTestEnvelope(t *testing.T, p resumeIntent) []byte {
	t.Helper()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(discoveryEnvelope{Payload: payload, SHA256: digestBytes(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestResumeIntentStrictEnvelope(t *testing.T) {
	p := testResumeIntent()
	raw := resumeTestEnvelope(t, p)
	decoded, e := decodeResumeIntent(raw, p.Home)
	if e != nil || !bytes.Equal(decoded.File.After, p.File.After) {
		t.Fatal("opaque file round trip failed")
	}
	if resumeIntentLimit > runtimepaths.MaxReadBytes {
		t.Fatal("intent exceeds native read ceiling")
	}
	// Decoded recovery evidence must not share a caller-owned file buffer.
	p.File.After[0] = 99
	if decoded.File.After[0] != 0 {
		t.Fatal("intent did not freeze its bytes")
	}
	for name, change := range map[string]func(*resumeIntent){
		"version":       func(p *resumeIntent) { p.Version++ },
		"home-format":   func(p *resumeIntent) { p.Home.FormatVersion++ },
		"home-id":       func(p *resumeIntent) { p.Home.DataHomeID = "dh_" + strings.Repeat("x", 32) },
		"home-inactive": func(p *resumeIntent) { p.Home.Initialized = false },
		"channel":       func(p *resumeIntent) { p.Home.Channel = "unrecognized" },
		"operation":     func(p *resumeIntent) { p.Operation = "archive" },
		"direction":     func(p *resumeIntent) { p.Operation = "delete" },
		"before-id":     func(p *resumeIntent) { p.Before.ResumeID = "another" },
		"after-id":      func(p *resumeIntent) { p.After.ResumeID = "another" },
		"present-hash":  func(p *resumeIntent) { p.After.SHA256 = "bad" },
		"absent-hash":   func(p *resumeIntent) { p.Before.SHA256 = p.After.SHA256 },
		"absent-bytes":  func(p *resumeIntent) { p.File.Before = []byte("old") },
		"empty-bytes":   func(p *resumeIntent) { p.File.After = nil },
		"row-direction": func(p *resumeIntent) { p.Before = p.After },
		"escape-path":   func(p *resumeIntent) { p.File.RelativePath = "../outside.pdf" },
		"extra-bytes":   func(p *resumeIntent) { p.File.After = make([]byte, resumelibrary.MaxResumeBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			p := testResumeIntent()
			change(&p)
			if _, e := decodeResumeIntent(resumeTestEnvelope(t, p), p.Home); e == nil || e.Code != "RESUME_INTENT_INVALID" {
				t.Fatal("invalid intent admitted")
			}
		})
	}
	for name, bad := range map[string][]byte{
		"trailing":  append(append([]byte(nil), raw...), ' '),
		"checksum":  bytes.Replace(raw, []byte(`"operation":"upload"`), []byte(`"operation":"attach"`), 1),
		"alias":     bytes.Replace(raw, []byte(`"payload"`), []byte(`"Payload"`), 1),
		"escape":    bytes.Replace(raw, []byte(`"payload"`), []byte(`"\u0070ayload"`), 1),
		"oversized": bytes.Repeat([]byte(" "), resumeIntentLimit+1),
		"deep":      []byte(strings.Repeat("[", 17) + "0" + strings.Repeat("]", 17)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := decodeResumeIntent(bad, p.Home); e == nil || e.Code != "RESUME_INTENT_INVALID" {
				t.Fatal("invalid envelope admitted")
			}
		})
	}
	// A correct digest still cannot excuse unknown or duplicate payload fields.
	for _, replacement := range []string{`"version":1,"version":1`, `"version":1,"private":"synthetic"`} {
		var env discoveryEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		env.Payload = bytes.Replace(env.Payload, []byte(`"version":1`), []byte(replacement), 1)
		env.SHA256 = digestBytes(env.Payload)
		bad, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		if _, e := decodeResumeIntent(bad, p.Home); e == nil {
			t.Fatal("re-signed unsupported payload admitted")
		}
	}
	other := p.Home
	other.Channel = "stable"
	if _, e := decodeResumeIntent(raw, other); e == nil || e.Code != "RESUME_INTENT_HOME_CHANGED" {
		t.Fatal("cross-home intent admitted")
	}
	// All three creation workflows and deletion have explicit witness directions.
	for _, op := range []string{"upload", "approved", "attach", "delete"} {
		p := testResumeIntent()
		p.Operation = op
		if op == "delete" {
			p.File.Before, p.File.After = p.File.After, nil
			p.File.BeforePresent, p.File.AfterPresent = true, false
			p.Before, p.After = p.After, p.Before
		}
		if _, e := decodeResumeIntent(resumeTestEnvelope(t, p), p.Home); e != nil {
			t.Fatal("valid direction refused", op, e.Code)
		}
	}
}
