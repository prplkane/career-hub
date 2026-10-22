package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/catalogtransaction"
	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
)

func TestExactVocabularyCommandStages(t *testing.T) {
	for _, scenario := range []string{"created", "reanalysis-noop", "existing", "non-draft", "canceled-after-create", "registry-failure", "registry-late-failure", "registry-unknown", "reanalysis-failure", "reanalysis-unknown", "reanalysis-partial", "reanalysis-cleanup", "reanalysis-recovery", "missing-witness"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := catalogwrite.ExactVocabularyRequest{JobID: "fictional-job", Name: "NebulaDB", PrimaryGroupID: "databases_storage", ConfirmedExact: true}
			r := &catalogwrite.ExactVocabularyResult{Operation: "created", JobID: request.JobID, SourceRef: "job:fictional-job#line:3",
				Record: map[string]any{"id": "user_nebuladb", "name": "NebulaDB"}, RegistryRevision: "synthetic-revision", JobDraft: true,
				CardFileSHA256: sha256.Sum256([]byte("card")), RawFileSHA256: sha256.Sum256([]byte("raw")), CatalogRevision: "semantic-catalog-revision"}
			publication := &catalogwrite.ArchiveOutcome{Write: catalogtransaction.Report{IntentRecorded: true, AppliedFiles: []string{"private-skills"}, FilesComplete: true, JournalRemoved: true, Completed: true}}
			calls, returned := 0, false
			c := &exactVocabularyCoordinator{
				add: func(_ context.Context, got catalogwrite.ExactVocabularyRequest) (*catalogwrite.ExactVocabularyOutcome, *catalogwrite.Error) {
					if got != request {
						t.Fatal("request changed")
					}
					defer func() { returned = true }()
					switch scenario {
					case "registry-unknown":
						return nil, nil
					case "registry-failure":
						return &catalogwrite.ExactVocabularyOutcome{Publication: &catalogwrite.ArchiveOutcome{}}, &catalogwrite.Error{Code: "SECRET_PRIVATE_TEXT"}
					case "registry-late-failure":
						publication.CleanupFailed = true
						return &catalogwrite.ExactVocabularyOutcome{Publication: publication}, &catalogwrite.Error{Committed: true}
					case "existing":
						r.Operation = "no_change_needed"
						publication = &catalogwrite.ArchiveOutcome{}
					case "non-draft":
						r.JobDraft = false
					case "canceled-after-create":
						cancel()
					case "missing-witness":
						r.RawFileSHA256 = [32]byte{}
					}
					return &catalogwrite.ExactVocabularyOutcome{Result: r, Publication: publication}, nil
				},
				reanalyze: func(_ context.Context, id, revision string, observed *vocabularyReanalysisWitness) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
					calls++
					if !returned || id != request.JobID || revision != "sha256:"+hex.EncodeToString(r.CardFileSHA256[:]) || observed == nil || observed.RawFileSHA256 != r.RawFileSHA256 || observed.CatalogRevision != r.CatalogRevision {
						t.Fatal("nested owner or lost source witness")
					}
					a := &httpapi.ApplicationOutcome{Result: json.RawMessage(`{"job":{"id":"fictional-job"}}`), Committed: true, RunID: "fictional-run"}
					switch scenario {
					case "reanalysis-unknown":
						return nil, nil
					case "reanalysis-failure":
						return a, &httpapi.ApplicationError{Code: "SECRET_PRIVATE_TEXT"}
					case "reanalysis-noop":
						a.Committed = false
					case "reanalysis-partial":
						a.Partial = true
					case "reanalysis-cleanup":
						a.CleanupFailed = true
					case "reanalysis-recovery":
						a.RecoveryRequired = true
					}
					return a, nil
				},
			}
			out, problem := c.Run(ctx, request)
			if strings.HasPrefix(scenario, "registry-") {
				if problem == nil || len(out.Receipt) != 0 || calls != 0 {
					t.Fatal("registry failure produced success/reanalysis")
				}
				if scenario == "registry-late-failure" && (!out.Registry.Committed || !out.Registry.CleanupFailed || !out.Registry.Write.Completed) {
					t.Fatal("lost late publication facts")
				}
				if scenario == "registry-unknown" && (!out.Registry.CommitUnknown || !out.Registry.RecoveryRequired) {
					t.Fatal("uncertainty hidden")
				}
				if strings.Contains(problem.Code, "SECRET") {
					t.Fatal("private failure escaped")
				}
				return
			}
			var receipt map[string]any
			if problem != nil || json.Unmarshal(out.Receipt, &receipt) != nil || strings.Contains(string(out.Receipt), "SECRET") || receipt["candidate_evidence_changed"] != false {
				t.Fatal("invalid or private receipt")
			}
			if scenario == "existing" {
				if len(receipt) != 7 || calls != 0 || out.Registry.Committed || receipt["result"] != "no_change_needed" {
					t.Fatal("no-op changed source contract")
				}
				return
			}
			if len(receipt) != 10 || receipt["result"] != "created" || !out.Registry.Committed || receipt["registry_revision"] != r.RegistryRevision {
				t.Fatal("creation lost")
			}
			success := scenario == "created" || scenario == "reanalysis-noop"
			if receipt["job_reanalyzed"] != success || (receipt["reanalysis_warning"] == nil) != success {
				t.Fatal("reanalysis falsely confirmed")
			}
			if scenario == "non-draft" || scenario == "canceled-after-create" || scenario == "missing-witness" {
				if calls != 0 || out.Reanalysis != nil {
					t.Fatal("unrequested reanalysis")
				}
			} else if calls != 1 {
				t.Fatal("reanalysis not attempted exactly once")
			}
			if scenario == "reanalysis-unknown" && (!out.Reanalysis.CommitUnknown || !out.Reanalysis.RecoveryRequired || out.Registry.CommitUnknown) {
				t.Fatal("stage uncertainty conflated")
			}
		})
	}
}
