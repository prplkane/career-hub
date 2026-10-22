package assessmentcontext

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/runtimepaths"
)

func TestReadOrderFixedPathsAndNoAmbientInputs(t *testing.T) {
	data, resources := &memoryReader{files: map[string][]byte{}}, &memoryReader{files: map[string][]byte{}}
	_, err := ReadRevisions(context.Background(), data, resources, Month{2026, 9})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{candidateFiles[0].path, candidateFiles[1].path, candidateFiles[2].path, candidateFiles[3].path, candidateFiles[4].path, contextFiles[1].path, contextFiles[2].path}
	if !reflect.DeepEqual(data.reads, append(append([]string(nil), want...), want...)) {
		t.Fatal("candidate read/recheck order changed or unrelated data accessed")
	}
	wantResources := []string{contextFiles[0].path, contextFiles[3].path, contextFiles[0].path, contextFiles[3].path}
	if !reflect.DeepEqual(resources.reads, wantResources) {
		t.Fatal("unexpected resource reads")
	}
}
func TestDriftAndReadFailuresDiscardCompleteResult(t *testing.T) {
	for _, mode := range []string{"presence", "bytes", "read", "limit", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data := &memoryReader{files: map[string][]byte{candidateFiles[0].path: []byte("{}")}}
			resources := &memoryReader{files: map[string][]byte{}}
			data.onRead = func(path string, count int) ([]byte, bool, *runtimepaths.Error, bool) {
				if count != 8 {
					return nil, false, nil, false
				} // First union recheck after seven data reads.
				switch mode {
				case "presence":
					return nil, false, nil, true
				case "bytes":
					return []byte("{} # changed"), true, nil, true
				case "read":
					return nil, false, &runtimepaths.Error{Code: "secret-path-do-not-forward"}, true
				case "limit":
					return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}, true
				case "canceled":
					cancel()
				}
				return nil, false, nil, false
			}
			got, err := ReadRevisions(ctx, data, resources, Month{2026, 9})
			if err == nil || !reflect.DeepEqual(got, Revisions{}) {
				t.Fatal("drift returned partial revisions")
			}
			for _, format := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(format, err), "secret") {
					t.Fatal("error formatting leaks a cause")
				}
			}
		})
	}
}
func TestEveryOperationIsCancelableAndMissingReaderFails(t *testing.T) {
	reader := &memoryReader{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadCandidateProjection(ctx, reader); err == nil {
		t.Fatal("canceled projection")
	}
	if _, err := ReadCandidateRevision(nil, reader); err == nil {
		t.Fatal("nil context")
	}
	if _, err := ReadContextRevision(ctx, reader, reader, Month{2026, 9}); err == nil {
		t.Fatal("canceled context")
	}
	if len(reader.reads) != 0 {
		t.Fatal("storage accessed after cancellation")
	}
	if _, err := ReadCandidateRevision(context.Background(), nil); err == nil || err.Code != "ASSESSMENT_ROOT_UNAVAILABLE" {
		t.Fatal("nil reader accepted")
	}
	for _, month := range []Month{{0, 1}, {10000, 1}, {2026, 0}, {2026, 13}} {
		if _, err := ReadRevisions(context.Background(), reader, reader, month); err == nil || err.Code != "ASSESSMENT_MONTH_INVALID" {
			t.Fatal("invalid month accepted")
		}
		if _, err := ReadContextRevision(context.Background(), reader, reader, month); err == nil {
			t.Fatal("invalid context month")
		}
	}
	if len(reader.reads) != 0 {
		t.Fatal("storage accessed for invalid month")
	}
}
func TestCanonicalBudgetsAndUnsupportedValues(t *testing.T) {
	for _, value := range []cataloginput.Value{{Kind: "date", Text: "2026-09-08"}, {Kind: "bytes"}, {Kind: "set"}, str(strings.Repeat("界", maxProjectionBytes/6+1))} {
		got, err := canonicalJSON(context.Background(), value)
		if err == nil || got != nil {
			t.Fatal("unsupported/over-budget canonical data accepted")
		}
	}
	deep := str("value")
	for range cataloginput.YAMLMaxDepth + 8 {
		deep = cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{deep}}
	}
	if got, err := canonicalJSON(context.Background(), deep); err == nil || got != nil {
		t.Fatal("depth budget ignored")
	}
	reader := &memoryReader{files: map[string][]byte{candidateFiles[0].path: []byte(strings.Repeat("x", cataloginput.YAMLMaxInputBytes+1))}}
	if _, err := ReadCandidateProjection(context.Background(), reader); err == nil || err.Code != "ASSESSMENT_INPUT_LIMIT" {
		t.Fatal("raw byte budget ignored")
	}
}
func TestParallelCallsOwnTheirProjection(t *testing.T) {
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			reader := &memoryReader{files: map[string][]byte{candidateFiles[2].path: []byte("[{id: first}]")}}
			first, err := ReadCandidateProjection(context.Background(), reader)
			if err != nil {
				t.Error(err)
				return
			}
			first.Entries[0].Value.Items[0].Entries[0].Value.Text = "changed"
			second, err := ReadCandidateProjection(context.Background(), reader)
			if err != nil {
				t.Error(err)
				return
			}
			if second.Entries[0].Value.Items[0].Entries[0].Value.Text != "first" {
				t.Error("shared caller state")
			}
		}()
	}
	wait.Wait()
}
