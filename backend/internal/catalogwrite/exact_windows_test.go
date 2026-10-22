//go:build windows

package catalogwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/runtimepaths"
)

type exactNativeRequest struct {
	Data, Resources, Mode string
	Request               ExactVocabularyRequest
}
type exactNativeResponse struct {
	Outcome *ExactVocabularyOutcome
	Problem *Error
}

func TestExactNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_EXACT_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r exactNativeRequest
	if json.NewDecoder(os.Stdin).Decode(&r) != nil {
		t.Fatal("fixture request")
	}
	root, e := runtimepaths.Open(r.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if r.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	s := New(r.Resources, r.Data)
	s.now = func() time.Time {
		path := ""
		switch r.Mode {
		case "changed-card":
			path = filepath.Join(r.Data, "jobs/normalized/"+r.Request.JobID+".yaml")
		case "changed-raw":
			loaded, e := jobcard.Read(context.Background(), root, "jobs/normalized/"+r.Request.JobID+".yaml")
			if e != nil {
				t.Fatal(e)
			}
			path = filepath.Join(r.Data, filepath.FromSlash(exactString(loaded.Card, "raw_jd_path")))
		case "changed-core":
			path = filepath.Join(r.Resources, catalogstore.CoreSkillsPath)
		case "changed-private":
			path = filepath.Join(r.Data, catalogstore.CustomSkillsPath)
		}
		if path != "" {
			raw, e := os.ReadFile(path)
			if e != nil && !os.IsNotExist(e) {
				t.Fatal(e)
			}
			putFixture(t, filepath.Dir(path), filepath.Base(path), append(raw, []byte("\n# independent fixture edit\n")...))
		}
		return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	}
	var out *ExactVocabularyOutcome
	var problem *Error
	if r.Mode == "late-witness" {
		out = &ExactVocabularyOutcome{}
		out.Publication, problem = s.runArchive(context.Background(), false, func(a *archiveSession, report *ArchiveOutcome) *Error {
			core := &archiveReadSet{a.resources, map[string]archiveObservation{}}
			private := &archiveReadSet{a.data, map[string]archiveObservation{}}
			state, e := catalogstore.ReadState(core, private)
			if e != nil {
				t.Fatal(e)
			}
			plan, e2 := planEdit(context.Background(), state, editRequest{operation: createSkill, payload: map[string]any{"name": "NebulaDB", "aliases": []any{}, "primary_group_id": "databases_storage"}, revision: state.Snapshot.Revision(), now: s.now()})
			if e2 != nil {
				t.Fatal(e2)
			}
			checks := 0
			return publishCatalogObserved(context.Background(), a, core, private, plan.replacements, plan.prospective, report, func() bool { checks++; return checks < 4 })
		})
	} else {
		out, problem = s.AddExactJobSkill(context.Background(), r.Request)
	}
	if json.NewEncoder(os.Stdout).Encode(exactNativeResponse{out, problem}) != nil {
		t.Fatal("fixture response")
	}
	os.Exit(0)
}
