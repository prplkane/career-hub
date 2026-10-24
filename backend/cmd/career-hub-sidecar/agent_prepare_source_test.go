package main

import (
	"encoding/json"
	"testing"

	"careerhub.local/backend/internal/applications/applicationwrite"
)

func TestPreparationFromIntakeRequiresCompletedFacts(t *testing.T) {
	for _, mode := range []string{"fresh", "duplicate", "unregistered", "nil", "pending", "file-unknown", "file-cleanup", "ledger-missing", "assessment-missing", "terminal-missing", "terminal-wrong-job", "terminal-wrong-operation", "applied", "duplicate-wrong-job", "duplicate-applied-intent", "duplicate-before-missing", "duplicate-before-invalid", "duplicate-unknown"} {
		t.Run(mode, func(t *testing.T) {
			p := &freshJobProgress{Files: &applicationwrite.NewJobFilePhaseOutcome{JobID: "job", OperationID: "operation", Complete: true, CardFileSHA256: [32]byte{1}, RawFileSHA256: [32]byte{2}}, Ledger: &applicationwrite.Outcome{Committed: true}, Assessment: &applicationwrite.Outcome{Committed: true}, Finalization: &applicationwrite.NewJobTerminalOutcome{JobID: "job", OperationID: "operation", RemovalObserved: true}}
			duplicate := mode == "duplicate" || mode == "unregistered" || len(mode) > 10 && mode[:10] == "duplicate-"
			if duplicate {
				p.Files.Duplicate = &applicationwrite.NewJobFileDuplicate{JobID: "job", InitialStatus: "captured"}
				p.Ledger, p.Assessment, p.Finalization = nil, nil, nil
				p.Duplicate = &applicationwrite.Outcome{Result: json.RawMessage(`{"job_id":"job","duplicate":true,"status_before":"preparing"}`)}
			}
			switch mode {
			case "nil":
				p = nil
			case "pending":
				p.RecoveryRequired = true
			case "file-unknown":
				p.Files.Unconfirmed = true
			case "file-cleanup":
				p.Files.CleanupFailed = true
			case "ledger-missing":
				p.Ledger = nil
			case "assessment-missing":
				p.Assessment = nil
			case "terminal-missing":
				p.Finalization = nil
			case "terminal-wrong-job":
				p.Finalization.JobID = "other"
			case "terminal-wrong-operation":
				p.Finalization.OperationID = "other"
			case "applied":
				p.Applied = &applicationwrite.Outcome{Committed: true}
			case "duplicate-wrong-job":
				p.Files.Duplicate.JobID = "other"
			case "duplicate-applied-intent":
				p.Files.Duplicate.InitialStatus = "applied"
			case "duplicate-before-missing":
				p.Duplicate.Result = json.RawMessage(`{"job_id":"job","duplicate":true}`)
			case "duplicate-before-invalid":
				p.Duplicate.Result = json.RawMessage(`{"job_id":"job","duplicate":true,"status_before":false}`)
			case "duplicate-unknown":
				p.Duplicate.CommitAttempted = true
			case "unregistered":
				p.Duplicate.Result = json.RawMessage(`{"job_id":"job","duplicate":true,"status_before":null}`)
			}
			job, binding, origin, ok := preparationFromIntake(p)
			valid := mode == "fresh" || mode == "duplicate" || mode == "unregistered"
			if ok != valid {
				t.Fatal("intake facts", mode, ok)
			}
			if valid {
				if job != "job" || origin.created != (mode == "fresh") || binding.CardSHA256 != ([32]byte{1}) || binding.RawSHA256 != ([32]byte{2}) {
					t.Fatal("wrong origin/binding")
				}
				if mode == "duplicate" {
					if origin.before == nil || *origin.before != "preparing" || binding.Status != "preparing" {
						t.Fatal("lost prior status")
					}
				} else if origin.before != nil || binding.Status != "captured" {
					t.Fatal("invented prior status")
				}
			}
		})
	}
}
