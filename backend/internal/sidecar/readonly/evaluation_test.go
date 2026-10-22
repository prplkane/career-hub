package readonly

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobevidence"
	"careerhub.local/backend/internal/runtimepaths"
)

type evaluationCase struct {
	Name, Error string
	Files       map[string]*string
	Expected    *struct {
		Index            jobevidence.Index
		DurationMonths   map[string]float64 `json:"duration_months"`
		Eligibility      json.RawMessage
		Expansions       map[string][]string
		SourceExpansions map[string][]string `json:"source_expansions"`
		Queries          []struct {
			Query    string
			Expected jobevidence.ConceptMatches
		}
	}
}

func evaluationValue(value cataloginput.Value) any {
	out := map[string]any{"kind": value.Kind}
	switch value.Kind {
	case "string", "int", "float", "date", "datetime":
		out["text"] = value.Text
	case "bytes":
		out["text"] = base64.StdEncoding.EncodeToString(value.Bytes)
	case "bool":
		out["value"] = value.Boolean
	case "sequence":
		items := []any{}
		for _, item := range value.Items {
			items = append(items, evaluationValue(item))
		}
		out["items"] = items
	case "mapping":
		entries := []any{}
		for _, entry := range value.Entries {
			entries = append(entries, []any{evaluationValue(entry.Key), evaluationValue(entry.Value)})
		}
		out["entries"] = entries
	}
	return out
}
func compareEvaluation(t *testing.T, test evaluationCase, got *EvaluationInputs, err error) {
	t.Helper()
	if test.Error != "" {
		requireCode(t, err, test.Error)
		if got != nil {
			t.Fatal("partial evidence returned")
		}
		return
	}
	if err != nil || got == nil || got.Candidate == nil {
		t.Fatalf("valid preparation failed: %v", err)
	}
	want := test.Expected
	if !reflect.DeepEqual(got.Candidate.Index, want.Index) || !reflect.DeepEqual(got.Candidate.DurationMonths, want.DurationMonths) || !reflect.DeepEqual(got.Expansions, want.Expansions) {
		t.Fatalf("composed evidence differs: %#v / %#v", got, want)
	}
	var eligibility any
	if json.Unmarshal(want.Eligibility, &eligibility) != nil || !reflect.DeepEqual(evaluationValue(got.Candidate.Eligibility), eligibility) {
		t.Fatal("eligibility values/order changed")
	}
	for _, query := range want.Queries {
		matches, err := jobevidence.FindConcepts(context.Background(), query.Query, got.Candidate.Index, got.Concepts)
		if err != nil || !reflect.DeepEqual(matches, query.Expected) {
			t.Fatalf("composed concept query differs: %q", query.Query)
		}
	}
}

type evaluationMemory struct {
	files         map[string][]byte
	reads, checks []string
	after         func(string)
	failure       string
}

func (m *evaluationMemory) Check(path string) *runtimepaths.Error {
	m.checks = append(m.checks, path)
	return nil
}
func (m *evaluationMemory) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if limit != cataloginput.YAMLMaxInputBytes {
		panic("unbounded evaluation read")
	}
	m.reads = append(m.reads, path)
	if m.failure != "" {
		return nil, false, &runtimepaths.Error{Code: m.failure}
	}
	raw, found := m.files[path]
	raw = bytes.Clone(raw)
	if m.after != nil {
		m.after(path)
	}
	return raw, found, nil
}
func evaluationMemories(files map[string]*string) (*evaluationMemory, *evaluationMemory) {
	data, resources := &evaluationMemory{files: map[string][]byte{}}, &evaluationMemory{files: map[string][]byte{}}
	for key, raw := range files {
		role, path, _ := strings.Cut(key, ":")
		target := data
		if role == "resource" {
			target = resources
		}
		if raw != nil {
			target.files[path] = []byte(*raw)
		}
	}
	return data, resources
}
func evaluationToday() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }

func TestEvaluationReadScopeAndCanceledInputs(t *testing.T) {
	ctx := context.Background()
	memory := &evaluationMemory{files: map[string][]byte{conceptsPath: []byte("[]")}}
	reader := &evidenceReader{ctx: ctx, source: memory, resource: true}
	for _, path := range []string{"documents/private.bin", "../profile.yaml", "career_vault/profile.yaml", "/absolute"} {
		if err := reader.Check(path); err == nil {
			t.Fatal("out-of-scope path checked")
		}
		if _, _, err := reader.ReadFile(path, cataloginput.YAMLMaxInputBytes); err == nil {
			t.Fatal("out-of-scope read")
		}
	}
	if len(memory.reads)+len(memory.checks) != 0 {
		t.Fatal("out-of-scope path touched storage")
	}
	if _, _, err := reader.ReadFile(conceptsPath, 1); err == nil {
		t.Fatal("unexpected bound accepted")
	}
	if _, _, err := reader.ReadFile(conceptsPath, cataloginput.YAMLMaxInputBytes); err != nil {
		t.Fatal(err)
	}
	memory.files[conceptsPath] = []byte("null")
	if _, _, err := reader.ReadFile(conceptsPath, cataloginput.YAMLMaxInputBytes); err == nil || err.Code != "EVIDENCE_INPUT_CHANGED" {
		t.Fatal("repeated read hid drift")
	}
	var w *Workspace
	for _, month := range []assessmentcontext.Month{{}, {Year: 2026, Number: 9}} {
		if got, err := w.PrepareJobEvidence(ctx, month); err == nil || got != nil {
			t.Fatal("invalid workspace/month accepted")
		}
	}
	if got, err := prepareJobEvidence(nil, nil, nil, evaluationToday()); err == nil || got != nil {
		t.Fatal("canceled composition accepted")
	}
	if got, err := prepareJobEvidence(ctx, nil, nil, evaluationToday()); err == nil || got != nil {
		t.Fatal("nil composition sources accepted")
	}
	if got, err := prepareJobEvidence(ctx, memory, memory, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil || got != nil {
		t.Fatal("out-of-source calendar admitted")
	}
	for _, mode := range []string{"read", "file-limit", "aggregate-limit"} {
		memory := &evaluationMemory{files: map[string][]byte{conceptsPath: []byte("[]")}}
		reader := &evidenceReader{ctx: ctx, source: memory, resource: true}
		switch mode {
		case "read":
			memory.failure = "private cause"
		case "file-limit":
			memory.files[conceptsPath] = make([]byte, cataloginput.YAMLMaxInputBytes+1)
		case "aggregate-limit":
			reader.bytes = 32 << 20
		}
		if raw, present, err := reader.ReadFile(conceptsPath, cataloginput.YAMLMaxInputBytes); err == nil || raw != nil || present {
			t.Fatal("read/size failure returned partial file")
		}
	}
}
