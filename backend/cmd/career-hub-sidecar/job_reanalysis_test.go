package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/jobs/jobdiagnostics"
	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
)

type reanalysisFixture struct {
	coordinator    *reanalysisCoordinator
	prior, planned [32]byte
	trace          []string
	events         []jobdiagnostics.Decoded
	appendFailure  string
	writeOutcome   *applicationwrite.Outcome
	writeError     *applicationwrite.Error
	last           *jobdiagnostics.Decoded
	observed       jobdiagnostics.PublicationState
	writes         int
}

func (f *reanalysisFixture) revision(hash [32]byte) string {
	return "sha256:" + hex.EncodeToString(hash[:])
}

func newReanalysisFixture(t *testing.T) *reanalysisFixture {
	t.Helper()
	f := &reanalysisFixture{prior: sha256.Sum256([]byte("prior fictional card")), planned: sha256.Sum256([]byte("next fictional card")),
		writeOutcome: &applicationwrite.Outcome{CommitAttempted: true, Committed: true}}
	f.coordinator = &reanalysisCoordinator{dataRoot: "fictional-home", ports: reanalysisPorts{
		home: func() (datahome.Info, error) { return datahome.Info{}, nil },
		last: func(context.Context, string, datahome.Info) (*jobdiagnostics.Decoded, *jobdiagnostics.StoreError) {
			f.trace = append(f.trace, "last")
			return f.last, nil
		},
		append: func(_ context.Context, _ string, _ datahome.Info, event jobdiagnostics.Event) (jobdiagnostics.AppendOutcome, *jobdiagnostics.StoreError) {
			decoded, err := jobdiagnostics.Decode(event.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			f.trace = append(f.trace, "append:"+decoded.Event)
			if f.appendFailure == decoded.Event {
				return jobdiagnostics.AppendOutcome{Uncertain: true}, &jobdiagnostics.StoreError{Code: "SYNTHETIC_APPEND"}
			}
			f.events = append(f.events, decoded)
			return jobdiagnostics.AppendOutcome{Committed: true}, nil
		},
		observe: func(context.Context, string, datahome.Info, jobdiagnostics.Decoded) (jobdiagnostics.PublicationState, *jobdiagnostics.StoreError) {
			f.trace = append(f.trace, "observe")
			return f.observed, nil
		},
		digest: func(_ context.Context, path string, options readonly.DigestOptions) (*readonly.RetainedJobDigest, error) {
			f.trace = append(f.trace, "digest")
			if path != "jobs/normalized/fictional-job.yaml" || !options.Reanalysis || options.Company != "" || options.Title != "" {
				t.Fatal("digest received an unseen source or hint")
			}
			return &readonly.RetainedJobDigest{Digest: &jobdigest.Result{}, CardFileSHA256: f.prior, PlannedCardSHA256: f.planned, CatalogRevision: hex.EncodeToString(f.prior[:])}, nil
		},
		write: func(_ context.Context, _ datahome.Info, id, revision string, prepared applicationwrite.ReanalysisPreparation) (*applicationwrite.Outcome, *applicationwrite.Error) {
			f.trace = append(f.trace, "write")
			f.writes++
			if id != "fictional-job" || revision != f.revision(f.prior) || prepared.CardFileSHA256 != f.prior || prepared.PlannedCardSHA256 != f.planned || prepared.Digest == nil {
				t.Fatal("writer lost the exact displayed revision or planned bytes")
			}
			return f.writeOutcome, f.writeError
		},
		saved: func(_ context.Context, id string) (*readonly.SavedJob, error) {
			f.trace = append(f.trace, "saved")
			if id != "fictional-job" {
				t.Fatal("wrong readback selector")
			}
			return &readonly.SavedJob{Card: map[string]any{"id": id, "card_revision": f.revision(f.planned)}}, nil
		},
	}}
	return f
}

func TestReanalysisCoordinatorPlanBeforePublication(t *testing.T) {
	f := newReanalysisFixture(t)
	out, problem := f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem != nil || !out.Committed || out.RunID == "" || f.writes != 1 {
		t.Fatalf("planned reanalysis failed: %#v %#v", out, problem)
	}
	want := []string{"last", "append:started", "digest", "append:planned", "write", "append:completed", "saved"}
	if !reflect.DeepEqual(f.trace, want) || len(f.events) != 3 || !f.events[1].HasPlan || f.events[1].NextCardSHA256 != f.planned || !f.events[2].Committed {
		t.Fatalf("publication sequence is not durable before write: %v %+v", f.trace, f.events)
	}
	var response map[string]any
	if json.Unmarshal(out.Result, &response) != nil || response["run_id"] != out.RunID || response["unchanged"] != false {
		t.Fatal("verified response lost run/changed state", response)
	}
}

func TestReanalysisVocabularyWitness(t *testing.T) {
	for _, scenario := range []string{"same", "raw-drift", "catalog-drift", "card-drift", "diagnostic-failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReanalysisFixture(t)
			observed := &vocabularyReanalysisWitness{CatalogRevision: hex.EncodeToString(f.prior[:])}
			revision := f.revision(f.prior)
			want := "JOB_REANALYSIS_INPUT_CHANGED"
			switch scenario {
			case "raw-drift":
				observed.RawFileSHA256 = sha256.Sum256([]byte("other source"))
			case "catalog-drift":
				observed.CatalogRevision = "other catalog"
			case "card-drift":
				revision, want = f.revision(f.planned), "JOB_REANALYSIS_REVISION_CONFLICT"
			case "diagnostic-failure":
				observed.CatalogRevision, f.appendFailure, want = "other catalog", "failed", "JOB_REANALYSIS_DIAGNOSTIC_FAILED"
			}
			out, problem := f.coordinator.reanalyze(context.Background(), "fictional-job", revision, observed)
			if scenario == "same" {
				if problem != nil || f.writes != 1 || !out.Committed {
					t.Fatal("matching witness refused")
				}
				return
			}
			if problem == nil || problem.Code != want || f.writes != 0 || out.Committed || len(out.Result) != 0 {
				t.Fatal("changed source reached writer")
			}
			if scenario == "diagnostic-failure" {
				if !out.RecoveryRequired {
					t.Fatal("diagnostic failure hidden")
				}
			} else if len(f.events) != 2 || f.events[1].Event != "failed" || f.events[1].CommitAttempted {
				t.Fatal("pre-publication failure not recorded")
			}
		})
	}
}

func TestReanalysisCoordinatorNoopAndPlanFailure(t *testing.T) {
	f := newReanalysisFixture(t)
	f.planned = f.prior
	f.writeOutcome = &applicationwrite.Outcome{}
	out, problem := f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem != nil || out.Committed || out.CommitUnknown || f.writes != 1 || len(f.events) != 2 || f.events[1].Sequence != 2 || f.events[1].HasPlan {
		t.Fatalf("no-op acquired a publication plan: %#v %#v %+v", out, problem, f.events)
	}
	if !reflect.DeepEqual(f.trace, []string{"last", "append:started", "digest", "write", "append:completed", "saved"}) {
		t.Fatal("no-op sequence", f.trace)
	}
	f = newReanalysisFixture(t)
	f.appendFailure = "planned"
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem == nil || problem.Code != "JOB_REANALYSIS_DIAGNOSTIC_FAILED" || !out.RecoveryRequired || f.writes != 0 || len(f.events) != 1 {
		t.Fatalf("write proceeded after unconfirmed plan: %#v %#v %+v", out, problem, f.events)
	}
}

func TestReanalysisCoordinatorUncertainAndPostcommitFailures(t *testing.T) {
	f := newReanalysisFixture(t)
	f.writeOutcome = &applicationwrite.Outcome{CommitAttempted: true}
	f.writeError = &applicationwrite.Error{Code: "JOB_REANALYSIS_WRITE_UNCERTAIN"}
	out, problem := f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem == nil || problem.Code != "JOB_REANALYSIS_WRITE_UNCERTAIN" || out.Committed || !out.CommitUnknown || !out.RecoveryRequired || len(f.events) != 3 || f.events[2].PublicationState != jobdiagnostics.PublicationUnknown {
		t.Fatalf("unconfirmed write looked rolled back: %#v %#v %+v", out, problem, f.events)
	}
	f = newReanalysisFixture(t)
	f.writeError = &applicationwrite.Error{Code: "JOB_REANALYSIS_COMMITTED_UNVERIFIED"}
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem == nil || problem.Code != "JOB_REANALYSIS_COMMITTED_UNVERIFIED" || !out.Committed || len(f.events) != 3 || !f.events[2].Committed || f.events[2].Event != "failed" {
		t.Fatalf("postcommit failure lost publication: %#v %#v %+v", out, problem, f.events)
	}
	f = newReanalysisFixture(t)
	f.appendFailure = "completed"
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem == nil || problem.Code != "JOB_REANALYSIS_DIAGNOSTIC_FAILED" || !out.Committed || !out.RecoveryRequired || !out.Partial || f.writes != 1 {
		t.Fatalf("terminal-log failure hid committed card: %#v %#v", out, problem)
	}
}

func TestReanalysisCoordinatorRecoveryAndStaleRevision(t *testing.T) {
	f := newReanalysisFixture(t)
	stale := f.revision(sha256.Sum256([]byte("unseen card")))
	out, problem := f.coordinator.Reanalyze(context.Background(), "fictional-job", stale)
	if problem == nil || problem.Code != "JOB_REANALYSIS_REVISION_CONFLICT" || !out.RevisionRefreshRequired || f.writes != 0 || len(f.events) != 2 || f.events[1].Event != "failed" {
		t.Fatalf("stale revision reached writer: %#v %#v %+v", out, problem, f.events)
	}
	run, err := jobdiagnostics.New("fictional-job")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Started(); err != nil {
		t.Fatal(err)
	}
	plan, err := run.Planned(f.prior, f.planned)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jobdiagnostics.Decode(plan.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	f = newReanalysisFixture(t)
	f.last, f.observed = &decoded, jobdiagnostics.PublicationUnknown
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem == nil || problem.Code != "JOB_REANALYSIS_RECOVERY_REQUIRED" || !out.RecoveryRequired || f.writes != 0 || len(f.events) != 1 || f.events[0].PublicationState != jobdiagnostics.PublicationUnknown {
		t.Fatalf("unknown interrupted run was resumed: %#v %#v %+v", out, problem, f.events)
	}
	f = newReanalysisFixture(t)
	f.last, f.observed = &decoded, jobdiagnostics.PublicationNotPublished
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem != nil || !out.Committed || f.writes != 1 || len(f.events) != 4 || f.events[0].PublicationState != jobdiagnostics.PublicationNotPublished {
		t.Fatalf("observed prior card could not start a fresh run: %#v %#v %+v", out, problem, f.events)
	}
	startedRun, err := jobdiagnostics.New("fictional-job")
	if err != nil {
		t.Fatal(err)
	}
	started, err := startedRun.Started()
	if err != nil {
		t.Fatal(err)
	}
	startedValue, err := jobdiagnostics.Decode(started.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	f = newReanalysisFixture(t)
	f.last = &startedValue
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem != nil || !out.Committed || len(f.events) != 4 || f.events[0].PublicationState != jobdiagnostics.PublicationNotPublished {
		t.Fatalf("started-only crash did not reconcile before a new run: %#v %#v %+v", out, problem, f.events)
	}
	unknownRun, err := jobdiagnostics.New("fictional-job")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unknownRun.Started(); err != nil {
		t.Fatal(err)
	}
	if _, err := unknownRun.Planned(f.prior, f.planned); err != nil {
		t.Fatal(err)
	}
	unknownEvent, err := unknownRun.Failed("JOB_REANALYSIS_WRITE_UNCERTAIN", true, false)
	if err != nil {
		t.Fatal(err)
	}
	unknownValue, err := jobdiagnostics.Decode(unknownEvent.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	f = newReanalysisFixture(t)
	f.last = &unknownValue
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem == nil || problem.Code != "JOB_REANALYSIS_RECOVERY_REQUIRED" || !out.RecoveryRequired || f.writes != 0 || len(f.events) != 0 {
		t.Fatalf("terminal unknown state was silently bypassed: %#v %#v %+v", out, problem, f.events)
	}
	f = newReanalysisFixture(t)
	f.last = decodedIntakeTerminal(t, true)
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem == nil || problem.Code != "JOB_REANALYSIS_RECOVERY_REQUIRED" || !out.RecoveryRequired || f.writes != 0 || len(f.events) != 0 {
		t.Fatalf("intake recovery state was bypassed by reanalysis: %#v %#v %+v", out, problem, f.events)
	}
	f = newReanalysisFixture(t)
	f.last, _ = decodedIntakeStarted(t)
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem != nil || !out.Committed || f.writes != 1 || len(f.events) != 4 ||
		f.events[0].ErrorCode != "JOB_INTAKE_INTERRUPTED" || f.events[0].RecoveryRequired {
		t.Fatalf("safe intake start tail did not close before reanalysis: %#v %#v %+v", out, problem, f.events)
	}
	f = newReanalysisFixture(t)
	f.last = decodedIntakeTerminal(t, false)
	out, problem = f.coordinator.Reanalyze(context.Background(), "fictional-job", f.revision(f.prior))
	if problem != nil || !out.Committed || f.writes != 1 || len(f.events) != 3 {
		t.Fatalf("settled intake tail blocked reanalysis: %#v %#v %+v", out, problem, f.events)
	}
}
