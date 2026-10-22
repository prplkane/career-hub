package jobcard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/runtimepaths"
)

const cardPath = "jobs/normalized/synthetic.yaml"
const rawPath = "jobs/incoming/synthetic.txt"

type memorySource struct {
	files                 map[string][]byte
	reads, checks         []string
	after                 func(string)
	checkError, readError string
	ghost                 bool
}

func newMemory() *memorySource {
	return &memorySource{files: map[string][]byte{cardPath: []byte(syntheticCard), rawPath: []byte("Build Python services.\r\nSecond line\rLast line\n")}}
}
func (m *memorySource) Check(path string) *runtimepaths.Error {
	m.checks = append(m.checks, path)
	if m.checkError != "" {
		return &runtimepaths.Error{Code: m.checkError}
	}
	return nil
}
func (m *memorySource) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if path != cardPath && path != rawPath {
		panic("out-of-scope read")
	}
	if path == cardPath && limit != cataloginput.YAMLMaxInputBytes || path == rawPath && limit != applicationanalysis.MaxSourceBytes {
		panic("wrong read bound")
	}
	m.reads = append(m.reads, path)
	if m.readError != "" {
		return nil, false, &runtimepaths.Error{Code: m.readError}
	}
	raw, present := m.files[path]
	raw = bytes.Clone(raw)
	if m.after != nil {
		m.after(path)
	}
	return raw, present && !m.ghost, nil
}
func loadFailure(t *testing.T, result *Loaded, err *Error, code string) {
	t.Helper()
	if result != nil || err == nil || err.Code != code {
		t.Fatalf("expected nil/%s got=%v error=%#v", code, result, err)
	}
}

func TestFreshReadSchemaIsNotProvenance(t *testing.T) {
	memory := newMemory()
	memory.files[cardPath] = append(memory.files[cardPath], []byte("content_fingerprint: '"+strings.Repeat("f", 64)+"'\n")...)
	result, err := Read(context.Background(), memory, cardPath)
	if err != nil || !result.Report.SchemaValid() || !result.RawPresent {
		t.Fatal("fresh schema failed")
	}
	if result.RawText != "Build Python services.\nSecond line\nLast line\n" || result.RawFileSHA256 != sha256.Sum256(memory.files[rawPath]) || result.CardFileSHA256 != sha256.Sum256(memory.files[cardPath]) {
		t.Fatal("exact bytes and text-mode decoding confused")
	}
	if strings.Join(memory.reads, ",") != strings.Join([]string{cardPath, rawPath, cardPath, rawPath}, ",") {
		t.Fatal("unexpected read scope or absent recheck")
	}
	delete(memory.files, rawPath)
	result, err = Read(context.Background(), memory, cardPath)
	if err != nil || result.Report.SchemaValid() || result.RawPresent || result.RawText != "" || len(result.Report.Issues) != 1 || result.Report.Issues[0].Message != "file does not exist" {
		t.Fatal("missing raw file was not a schema issue")
	}
}

func TestReadsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(*memorySource)
	}{
		{"missing-card", "JOB_CARD_NOT_FOUND", func(m *memorySource) { delete(m.files, cardPath) }},
		{"check-error", "JOB_CARD_SOURCE_UNAVAILABLE", func(m *memorySource) { m.checkError = "PRIVATE" }},
		{"read-error", "JOB_CARD_SOURCE_UNAVAILABLE", func(m *memorySource) { m.readError = "PRIVATE" }},
		{"bounded-provider", "JOB_CARD_INPUT_LIMIT", func(m *memorySource) { m.readError = "FILE_SIZE_LIMIT" }},
		{"ghost-bytes", "JOB_CARD_SOURCE_UNAVAILABLE", func(m *memorySource) { m.ghost = true }},
		{"large-card", "JOB_CARD_INPUT_LIMIT", func(m *memorySource) { m.files[cardPath] = make([]byte, cataloginput.YAMLMaxInputBytes+1) }},
		{"large-raw", "JOB_CARD_INPUT_LIMIT", func(m *memorySource) { m.files[rawPath] = make([]byte, applicationanalysis.MaxSourceBytes+1) }},
		{"bad-yaml", "JOB_CARD_DECODE_FAILED", func(m *memorySource) { m.files[cardPath] = []byte("[invalid") }},
		{"bad-card-utf8", "JOB_CARD_DECODE_FAILED", func(m *memorySource) { m.files[cardPath] = []byte{255} }},
		{"bad-raw-utf8", "JOB_CARD_SOURCE_ENCODING", func(m *memorySource) { m.files[rawPath] = []byte{255} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			memory := newMemory()
			test.change(memory)
			result, err := Read(context.Background(), memory, cardPath)
			loadFailure(t, result, err, test.code)
		})
	}
	for _, path := range []string{"../private.yaml", "career_vault/profile.yaml", "jobs/normalized/../incoming/test.yaml", "jobs/normalized/test.yaml:stream", "jobs/normalized/test.txt", "jobs/normalized/\x00.yaml", "jobs/normalized/a/", "jobs/normalized/a\\..\\test.yaml", "C:\\jobs\\normalized\\a.yaml", "jobs/normalized/" + string([]byte{255}) + ".yaml"} {
		memory := newMemory()
		result, err := Read(context.Background(), memory, path)
		loadFailure(t, result, err, "JOB_CARD_PATH_INVALID")
		if len(memory.reads)+len(memory.checks) != 0 {
			t.Fatal("invalid path accessed disk")
		}
	}
	result, err := Read(context.Background(), nil, cardPath)
	loadFailure(t, result, err, "JOB_CARD_SOURCE_UNAVAILABLE")
	result, err = Read(nil, newMemory(), cardPath)
	loadFailure(t, result, err, "JOB_CARD_CANCELED")
}

func TestChangedReadsAndCancellationDiscardEverything(t *testing.T) {
	for _, path := range []string{cardPath, rawPath} {
		for _, mutation := range []string{"change", "remove", "appear"} {
			t.Run(path+"/"+mutation, func(t *testing.T) {
				memory := newMemory()
				if mutation == "appear" {
					if path == cardPath {
						return
					}
					delete(memory.files, path)
				}
				memory.after = func(observed string) {
					if observed != rawPath {
						return
					}
					memory.after = nil
					switch mutation {
					case "change":
						memory.files[path] = append(memory.files[path], ' ')
					case "remove":
						delete(memory.files, path)
					case "appear":
						memory.files[path] = []byte("new")
					}
				}
				result, err := Read(context.Background(), memory, cardPath)
				loadFailure(t, result, err, "JOB_CARD_INPUT_CHANGED")
			})
		}
	}
	baseline := &cancelAfter{Context: context.Background()}
	result, err := Read(baseline, newMemory(), cardPath)
	if err != nil || !result.Report.SchemaValid() {
		t.Fatal("baseline failed")
	}
	for at := 1; at <= baseline.calls; at++ {
		ctx := &cancelAfter{Context: context.Background(), at: at}
		result, err := Read(ctx, newMemory(), cardPath)
		loadFailure(t, result, err, "JOB_CARD_CANCELED")
	}
	t.Logf("fresh read cancellation boundaries=%d", baseline.calls)
}
