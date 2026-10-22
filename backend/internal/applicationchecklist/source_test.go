package applicationchecklist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/runtimepaths"
)

const fixtureText = "Private synthetic source\nHow to apply\n- Write  \"I READ\" in your cover letter.\nManual\twords.\n"

type memorySource struct {
	files  map[string][]byte
	calls  []string
	fault  string
	cancel context.CancelFunc
}

func (s *memorySource) ReadFile(path string, _ int64) ([]byte, bool, *runtimepaths.Error) {
	s.calls = append(s.calls, path)
	if s.cancel != nil {
		s.cancel()
	}
	if s.fault != "" {
		return nil, false, &runtimepaths.Error{Code: s.fault}
	}
	raw, present := s.files[path]
	return raw, present, nil
}
func fixtureSource(t *testing.T, text string, change func(map[string]any)) *memorySource {
	t.Helper()
	analysis, err := applicationanalysis.ExtractInstructions(text)
	if err != nil {
		t.Fatal(err)
	}
	card := map[string]any{"id": "job-one", "content_fingerprint": analysis.SourceFingerprint, "raw_jd_path": "jobs/incoming/job-one.txt", "digest": map[string]any{"application_instructions": analysis}}
	if change != nil {
		change(card)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	return &memorySource{files: map[string][]byte{"jobs/normalized/job-one.yaml": raw, "jobs/incoming/job-one.txt": []byte(text)}}
}
func loadRequest() Request { return Request{"load", map[string]any{"job_id": "job-one"}} }

func TestSourceBoundedReadsAndPrivateFailures(t *testing.T) {
	for _, name := range []string{"valid", "card-limit", "raw-limit", "card-utf8", "raw-utf8", "source-changed", "source-missing", "fault", "canceled", "provider-canceled", "yaml-recursive", "yaml-surrogate"} {
		t.Run(name, func(t *testing.T) {
			s := fixtureSource(t, fixtureText, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			code := ""
			switch name {
			case "card-limit":
				s.files["jobs/normalized/job-one.yaml"] = make([]byte, cataloginput.YAMLMaxInputBytes+1)
				code = "INPUT_LIMIT"
			case "raw-limit":
				s.files["jobs/incoming/job-one.txt"] = make([]byte, applicationanalysis.MaxSourceBytes+1)
				code = "INPUT_LIMIT"
			case "card-utf8":
				s.files["jobs/normalized/job-one.yaml"] = []byte{0xff}
				code = "SOURCE_ERROR"
			case "raw-utf8":
				s.files["jobs/incoming/job-one.txt"] = []byte{0xff}
				code = "SOURCE_ERROR"
			case "source-changed":
				s.files["jobs/incoming/job-one.txt"] = []byte("private changed bytes")
				code = "SOURCE_FINGERPRINT_MISMATCH"
			case "source-missing":
				delete(s.files, "jobs/incoming/job-one.txt")
				code = "SOURCE_ERROR"
			case "fault":
				s.fault = "UNTRUSTED_PRIVATE_PROVIDER_TEXT"
				code = "SOURCE_ERROR"
			case "canceled":
				cancel()
				code = "CANCELED"
			case "provider-canceled":
				s.cancel = cancel
				code = "CANCELED"
			case "yaml-recursive":
				s.files["jobs/normalized/job-one.yaml"] = []byte("a: &cycle [*cycle]")
				code = "CARD_REPRESENTATION_UNSUPPORTED"
			case "yaml-surrogate":
				s.files["jobs/normalized/job-one.yaml"] = []byte(`private: "\ud800"`)
				code = "CARD_REPRESENTATION_UNSUPPORTED"
			}
			p, err := Prepare(ctx, s, loadRequest())
			if code == "" {
				if err != nil || p == nil || len(s.calls) != 2 {
					t.Fatal("expected confined complete source", err)
				}
				if strings.Contains(fmt.Sprintf("%#v", p), "Private synthetic") {
					t.Fatal("private prepared value formatted")
				}
			} else if p != nil || err == nil || err.Code != "APPLICATION_CHECKLIST_"+code {
				t.Fatal("wrong refusal", err)
			}
			if err != nil && strings.Contains(fmt.Sprintf("%+v %#v", err, err), "PRIVATE") {
				t.Fatal("private cause formatted")
			}
		})
	}
}

func TestSourceRequestAndSubtreeBoundary(t *testing.T) {
	for _, value := range []any{map[string]any{"secret": "private"}, []any{}, json.Number("null"), json.Number("1e2"), strings.Repeat("x", 16385), "bad\xff"} {
		request := loadRequest()
		request.Fields["job_id"] = value
		s := fixtureSource(t, fixtureText, nil)
		p, err := Prepare(context.Background(), s, request)
		if p != nil || err == nil || len(s.calls) != 0 {
			t.Fatal("unsupported request reached source")
		}
	}
	for _, path := range []string{"https://example.invalid", "jobs/normalized/other.yaml", "jobs/incoming/../secret", "jobs/incoming//job-one.txt", "/jobs/incoming/job-one.txt", "jobs/incoming/./job-one.txt"} {
		s := fixtureSource(t, fixtureText, func(card map[string]any) { card["raw_jd_path"] = path })
		p, err := Prepare(context.Background(), s, loadRequest())
		if p != nil || err == nil || len(s.calls) != 1 {
			t.Fatal("unconfined source path read")
		}
	}
	r := loadRequest()
	r.Fields["secret"] = "PRIVATE"
	s := fixtureSource(t, fixtureText, nil)
	if p, err := Prepare(context.Background(), s, r); p != nil || err == nil || len(s.calls) != 0 {
		t.Fatal("unknown field admitted")
	}
	if strings.Contains(fmt.Sprintf("%#v", r), "PRIVATE") {
		t.Fatal("request formatted")
	}
}

func TestExactEvidenceAndBoundedLineWork(t *testing.T) {
	p, err := Prepare(context.Background(), fixtureSource(t, fixtureText, nil), loadRequest())
	if err != nil {
		t.Fatal(err)
	}
	items := field(p.analysis, "items").Items
	if len(items) == 0 {
		t.Fatal("missing actual analyzer fixture")
	}
	raw, err := yamlItem(items[0])
	if err != nil {
		t.Fatal(err)
	}
	item, err := publicItem(raw, "detected")
	if err != nil {
		t.Fatal(err)
	}
	e := engine{ctx: context.Background(), prepared: p}
	if err := e.evidence(item, true); err != nil {
		t.Fatal("actual collapsed/bullet evidence refused", err)
	}
	item["line_start"], item["line_end"] = int64(1), int64(1)
	if err := e.evidence(item, true); err == nil || err.Code != "APPLICATION_CHECKLIST_EVIDENCE_MISMATCH" {
		t.Fatal("wrong line accepted")
	}
	text := strings.Repeat("z", 1200)
	fp, er := applicationanalysis.InstructionFingerprint("external_step", text)
	if er != nil {
		t.Fatal(er)
	}
	item = map[string]any{"source_text": text, "fingerprint": fp, "category": "external_step", "line_start": int64(1), "line_end": int64(1)}
	e = engine{ctx: context.Background(), prepared: &Prepared{text: strings.Repeat("z", 4<<20)}}
	if err := e.evidence(item, false); err != nil {
		t.Fatal(err)
	}
	if err := e.evidence(item, false); err != nil {
		t.Fatal(err)
	}
	if err := e.evidence(item, false); err == nil || err.Code != "APPLICATION_CHECKLIST_EVIDENCE_WORK_LIMIT" {
		t.Fatal("unbounded repeated span work")
	}
	e = engine{ctx: context.Background(), prepared: &Prepared{text: strings.Repeat("\n", 100001)}}
	if err := e.evidence(item, false); err == nil || err.Code != "APPLICATION_CHECKLIST_SOURCE_LINE_LIMIT" {
		t.Fatal("unbounded line allocation")
	}
}

func TestChecklistPrimitiveRequiresPreparedOwnedTransaction(t *testing.T) {
	var p *Prepared
	if out, err := p.ApplyInTransaction(context.Background(), nil, ""); out != nil || err == nil {
		t.Fatal("nil preparation accepted")
	}
	r := loadRequest()
	s := fixtureSource(t, fixtureText, nil)
	p, err := Prepare(context.Background(), s, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Fields["job_id"] = "different"
	if p.id != "job-one" || p.request.Fields["job_id"] != "job-one" {
		t.Fatal("request not snapshotted")
	}
	if out, err := p.ApplyInTransaction(context.Background(), nil, ""); out != nil || err == nil {
		t.Fatal("no transaction accepted")
	}
	for _, text := range []string{"", " \u001c ", "\xff", strings.Repeat("😀", 1201)} {
		if _, err := applicationanalysis.InstructionFingerprint("external_step", text); err == nil {
			t.Fatal("unsupported fingerprint input accepted")
		}
	}
}
