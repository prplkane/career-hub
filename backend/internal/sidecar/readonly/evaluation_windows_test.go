//go:build windows

package readonly

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobevidence"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
)

type evaluationRequest struct {
	Start protocol.StartRecord
	Mode  string
	Cases []evaluationCase
}

func installEvaluationCase(t *testing.T, start protocol.StartRecord, test evaluationCase) {
	t.Helper()
	if len(test.Files) != 13 {
		t.Fatal("synthetic fixed-file scope drift")
	}
	for key, raw := range test.Files {
		role, relative, found := strings.Cut(key, ":")
		if !found || role != "data" && role != "resource" {
			t.Fatal("unexpected synthetic file role")
		}
		reader := evidenceReader{resource: role == "resource"}
		if !reader.allowed(relative) {
			t.Fatal("out-of-scope fixture path")
		}
		root := start.DataRoot
		if reader.resource {
			root = start.ResourceRoot
		}
		if raw == nil {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil && !os.IsNotExist(err) {
				t.Fatal("fixed synthetic file removal failed")
			}
		} else {
			writeFixture(t, root, relative, *raw)
		}
	}
}

func TestEvaluationProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_EVALUATION_HELPER") != "1" {
		return
	}
	var request evaluationRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 4<<20)).Decode(&request) != nil || len(request.Cases) == 0 || len(request.Cases) > 15 {
		t.Fatal("invalid synthetic request")
	}
	var w *Workspace
	var err error
	if request.Mode == "unowned" {
		w, err = Open(request.Start)
	} else {
		w, err = OpenForProcess(request.Start)
	}
	if err != nil {
		t.Fatal("synthetic workspace admission failed")
	}
	base := filepath.Dir(request.Start.DataRoot)
	writeFixture(t, request.Start.DataRoot, "documents/private.bin", "Synthetic private document: not evidence input")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	month := assessmentcontext.Month{Year: 2026, Number: 9}
	if request.Mode == "corpus" {
		for _, test := range request.Cases {
			installEvaluationCase(t, request.Start, test)
			t.Run(test.Name, func(t *testing.T) {
				before := tree(t, base)
				got, err := w.PrepareJobEvidence(ctx, month)
				compareEvaluation(t, test, got, err)
				if !reflect.DeepEqual(before, tree(t, base)) {
					t.Fatal("owned evaluation modified files")
				}
			})
		}
		return
	}
	installEvaluationCase(t, request.Start, request.Cases[0])
	want := ""
	switch request.Mode {
	case "unowned":
		want = "ASSESSMENT_OWNERSHIP_REQUIRED"
	case "identity":
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker("dev", "dh_22222222222222222222222222222222"))
		want = "DATA_HOME_IDENTITY_CHANGED"
	case "recovery":
		writeFixture(t, request.Start.DataRoot, datahome.DocumentRecoveryPath, "synthetic recovery")
		want = "DATA_HOME_RECOVERY_REQUIRED"
	case "resources":
		writeFixture(t, request.Start.ResourceRoot, "VERSION", "invalid")
		want = "SIDECAR_RESOURCE_INVALID"
	case "canceled":
		cancel()
		want = "ASSESSMENT_INPUT_CANCELED"
	case "directory":
		path := filepath.Join(request.Start.ResourceRoot, filepath.FromSlash(conceptsPath))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		want = "EVIDENCE_READ_FAILED"
	}
	before := tree(t, base)
	got, err := w.PrepareJobEvidence(ctx, month)
	if want != "" {
		requireCode(t, err, want)
		if got != nil {
			t.Fatal("native failure returned partial evidence")
		}
	} else {
		compareEvaluation(t, request.Cases[0], got, err)
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("owned evaluation changed native files")
	}
	if request.Mode == "refresh" {
		writeFixture(t, request.Start.DataRoot, "career_vault/experience_blocks.yaml", "- {id: exp, company: Synthetic, role: Engineer, dates: '2020 - 2020', category: technical, technologies: [Rust], proof_points: []}\n")
		writeFixture(t, request.Start.ResourceRoot, conceptsPath, "- {id: changed, aliases: [Data engineering], evidence_terms: [Rust]}\n")
		writeFixture(t, request.Start.DataRoot, catalogstore.CustomSkillsPath, "- {id: extra_db, name: Extra DB, category: database, parent: sql}\n")
		before = tree(t, base)
		next, err := w.PrepareJobEvidence(ctx, month)
		if err != nil || next == nil || next.Candidate.DurationMonths["exp"] != 12 || reflect.DeepEqual(next.Expansions, got.Expansions) {
			t.Fatal("fresh private/resource input was cached")
		}
		match, failure := jobevidence.FindConcepts(ctx, "Data engineering", next.Candidate.Index, next.Concepts)
		if failure != nil || !reflect.DeepEqual(match.ConceptIDs, []string{"changed"}) || !reflect.DeepEqual(match.Direct, []string{"exp"}) {
			t.Fatal("fresh concept/evidence association lost")
		}
		if !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("fresh read modified files")
		}
	}
}
