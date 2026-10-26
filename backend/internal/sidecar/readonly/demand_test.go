package readonly

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/demandgaps"
)

func TestDemandObservedBytesCancelAndLimit(t *testing.T) {
	for _, mode := range []string{"drift", "disappeared", "read-failure", "canceled", "budget"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			memory := &evaluationMemory{files: map[string][]byte{"jobs/normalized/job.yaml": []byte("fixture")}}
			reader := &demandReader{ctx: ctx, root: memory, seen: map[string]demandObservation{}}
			if _, _, e := reader.ReadFile("jobs/normalized/job.yaml", cataloginput.YAMLMaxInputBytes); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "drift":
				memory.files["jobs/normalized/job.yaml"] = []byte("changed")
			case "disappeared":
				delete(memory.files, "jobs/normalized/job.yaml")
			case "read-failure":
				memory.failure = "private-marker"
			case "canceled":
				cancel()
			case "budget":
				reader.bytes = 64 << 20
				if _, _, e := reader.ReadFile("jobs/normalized/job.yaml", cataloginput.YAMLMaxInputBytes); e == nil {
					t.Fatal("aggregate limit ignored")
				}
				return
			}
			if reader.verify() == nil {
				t.Fatal("changed input accepted")
			}
		})
	}
}

// Captured from the immutable pre-retirement reference. Ordinary native tests
// replay complete payloads (including revision digests) without Python.
func TestDemandCapturedSourceContract(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "demand_contract.json"))
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Name  string
			Files map[string]*string
			Jobs  []struct {
				ID                string
				CapturedAt        string `json:"captured_at"`
				Fingerprint, Card string
			}
			Normalized int
			Request    demandgaps.Request
			Expected   json.RawMessage
			Error      bool
		}
	}
	if json.Unmarshal(raw, &corpus) != nil || corpus.SourceCommit != "3fd141c809e68dd2f2cc60ef325a98e665c6bdb1" || len(corpus.Cases) != 16 {
		t.Fatal("source fixture identity changed")
	}
	for _, tc := range corpus.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			data, resources := evaluationMemories(tc.Files)
			before, _ := json.Marshal([]any{data.files, resources.files})
			evidence, e := prepareCompetencies(context.Background(), data, resources, tc.Request.AsOf, false)
			if e != nil {
				t.Fatal(e)
			}
			registry, problem := catalogstore.ReadState(resources, data)
			if problem != nil {
				t.Fatal(problem)
			}
			jobs := []demandgaps.Job{}
			for _, item := range tc.Jobs {
				card, e := cataloginput.ParseYAML([]byte(item.Card))
				if e != nil {
					t.Fatal(e)
				}
				jobs = append(jobs, demandgaps.Job{ID: item.ID, CapturedAt: item.CapturedAt, Fingerprint: item.Fingerprint, Card: card})
			}
			result, failure := demandgaps.Build(context.Background(), registry.Snapshot, evidence, jobs, tc.Normalized, len(jobs), tc.Request)
			if tc.Error {
				if failure == nil || result != nil {
					t.Fatal("invalid source accepted")
				}
				return
			}
			if failure != nil {
				t.Fatal(failure)
			}
			var got, want any
			_ = json.Unmarshal(result, &got)
			_ = json.Unmarshal(tc.Expected, &want)
			if !reflect.DeepEqual(got, want) {
				for key, expected := range want.(map[string]any) {
					if !reflect.DeepEqual(got.(map[string]any)[key], expected) {
						t.Errorf("field %s differs: got %v; want %v", key, got.(map[string]any)[key], expected)
					}
				}
				t.FailNow()
			}
			after, _ := json.Marshal([]any{data.files, resources.files})
			if string(before) != string(after) {
				t.Fatal("projection wrote source")
			}
		})
	}
}
