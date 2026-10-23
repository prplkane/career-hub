//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobdiagnostics"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const freshJobChild = "CAREERHUB_TEST_FRESH_JOB_CAPTURED_CHILD"
const freshJobRoot = "CAREERHUB_TEST_FRESH_JOB_CAPTURED_ROOT"
const freshJobCase = "CAREERHUB_TEST_FRESH_JOB_CAPTURED_CASE"

func TestFreshJobCapturedOwnedComposition(t *testing.T) {
	if os.Getenv(freshJobChild) == "1" {
		runFreshJobCaptured(t, os.Getenv(freshJobRoot), os.Getenv(freshJobCase))
		return
	}
	for _, scenario := range []string{"captured", "missing_schema", "assessment_unavailable", "assessment_success", "captured_finalized", "diagnostic", "http", "applied", "duplicate", "recovery", "recovery_applied"} {
		t.Run(scenario, func(t *testing.T) {
			base := newJobHostFixture(t)
			if scenario == "assessment_success" || scenario == "captured_finalized" || scenario == "diagnostic" || scenario == "http" || scenario == "applied" || scenario == "duplicate" ||
				scenario == "recovery" || scenario == "recovery_applied" {
				installFreshJobAssessmentFixture(t, base)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFreshJobCapturedOwnedComposition$")
			child.Env = append(os.Environ(), freshJobChild+"=1", freshJobRoot+"="+base, freshJobCase+"="+scenario)
			child.WaitDelay = time.Second
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("fictional fresh-job %s: %v %.1600s", scenario, err, output)
			}
			reopened := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFreshJobRecoveryReopen$")
			reopened.Env = append(os.Environ(), freshJobRoot+"="+base, freshJobCase+"="+scenario)
			reopened.WaitDelay = time.Second
			if output, err := reopened.CombinedOutput(); err != nil {
				t.Fatalf("fictional fresh-process recovery %s: %v %.1600s", scenario, err, output)
			}
		})
	}
}

func TestFreshJobRecoveryReopen(t *testing.T) {
	base := os.Getenv(freshJobRoot)
	if base == "" {
		t.Skip("fresh-process helper only")
	}
	data := filepath.Join(base, "data")
	root, openErr := runtimepaths.Open(data)
	if openErr != nil {
		t.Fatal(openErr.Code)
	}
	if claimErr := root.ClaimForProcess(); claimErr != nil {
		t.Fatal(claimErr.Code)
	}
	defer root.Close()
	scenario := os.Getenv(freshJobCase)
	if scenario == "captured_finalized" || scenario == "diagnostic" || scenario == "http" || scenario == "applied" || scenario == "duplicate" ||
		scenario == "recovery" || scenario == "recovery_applied" {
		if _, inspectErr := datahome.Inspect(data, nil); inspectErr != nil {
			t.Fatal("finalized job still blocks ordinary admission", inspectErr.Code)
		}
		if _, err := os.Stat(filepath.Join(data, datahome.NewJobRecoveryPath)); !os.IsNotExist(err) {
			t.Fatal("finalized job retained pending intent", err)
		}
		return
	}
	home, inspectErr := datahome.InspectOwnedForNewJobRecovery(root, nil)
	if inspectErr != nil {
		t.Fatal(inspectErr.Code)
	}
	observed, problem := applicationwrite.New(data).PinnedToHome(*home).ObserveNewJobRecovery(context.Background())
	want := map[string]string{"captured": "captured", "missing_schema": "database_missing",
		"assessment_unavailable": "captured", "assessment_success": "assessed", "applied": "applied"}[scenario]
	if problem != nil || observed == nil || observed.FileState != "complete" || observed.LedgerState != want {
		t.Fatal("fresh-process state classification failed", scenario, observed, problem)
	}
}

func installFreshJobAssessmentFixture(t *testing.T, base string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "saved_assessment_home.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceCommit string `json:"source_commit"`
		Files        map[string]string
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.SourceCommit != "3fd141c809e68dd2f2cc60ef325a98e665c6bdb1" {
		t.Fatal("invalid captured fixture")
	}
	for key, value := range fixture.Files {
		role, path, ok := strings.Cut(key, ":")
		if role == "resource" {
			role = "resources"
		}
		if !ok || (role != "data" && role != "resources") || !filepath.IsLocal(path) {
			t.Fatal("invalid fixture path")
		}
		materialFixtureFile(t, filepath.Join(base, role, filepath.FromSlash(path)), []byte(value))
	}
}

func runFreshJobCaptured(t *testing.T, base, scenario string) {
	t.Helper()
	data, resources := filepath.Join(base, "data"), filepath.Join(base, "resources")
	start := protocol.StartRecord{DataRoot: data, ResourceRoot: resources, Channel: "dev"}
	workspace, err := readonly.OpenForProcess(start)
	if err != nil {
		t.Fatal("synthetic process admission", err)
	}
	if scenario != "missing_schema" {
		prepared, err := schema.PrepareOwned(context.Background(), data)
		if err != nil || prepared == nil || !prepared.Committed {
			t.Fatal("synthetic SQLite preparation", err)
		}
	}
	request := readonly.NewJobRequest{SourceName: "fictional_job.txt", RawText: reanalysisCoordinatorSource,
		SourceURL: "https://example.test/jobs/new", IdempotencyKey: "fictional-key"}
	if scenario == "http" {
		runFreshJobIntakeHTTP(t, start, workspace, request)
		return
	}
	if scenario == "duplicate" {
		runDuplicateJobIntake(t, data, start, workspace, request)
		return
	}
	if scenario == "recovery" || scenario == "recovery_applied" {
		runRecoveredJobIntake(t, data, start, workspace, request, scenario == "recovery_applied")
		return
	}
	if scenario == "applied" {
		request.InitialStatus = "applied"
	}
	var progress *freshJobProgress
	var problem *applicationwrite.Error
	if scenario == "diagnostic" {
		progress, problem = newIntakeDiagnosticCoordinator(workspace, start).Intake(context.Background(), request,
			"Fictional Labs", "Backend Engineer", assessmentcontext.Month{Year: 2026, Number: 9})
	} else if scenario == "assessment_unavailable" || scenario == "captured_finalized" || scenario == "applied" {
		progress, problem = intakeFreshNewJob(context.Background(), workspace, start, request,
			"Fictional Labs", "Backend Engineer", assessmentcontext.Month{Year: 2026, Number: 9})
	} else if scenario == "assessment_success" {
		progress, problem = captureAndAssessFreshNewJob(context.Background(), workspace, start, request,
			"Fictional Labs", "Backend Engineer", assessmentcontext.Month{Year: 2026, Number: 9})
	} else {
		progress, problem = registerFreshNewJob(context.Background(), workspace, start, request, "Fictional Labs", "Backend Engineer")
	}
	if progress == nil || progress.Files == nil || !progress.Files.Complete || !progress.Files.RecoveryRequired ||
		progress.Files.JobID != "fictional_job" || progress.Ledger == nil || !progress.Ledger.RecoveryRequired {
		t.Fatal("file and SQL phase facts were conflated", progress, problem)
	}
	home, err := workspace.StartupHome()
	if err != nil {
		t.Fatal(err)
	}
	if scenario == "captured_finalized" || scenario == "diagnostic" || scenario == "applied" {
		if problem != nil || progress.Finalization == nil || !progress.Finalization.RemovalReported ||
			!progress.Finalization.RemovalObserved || progress.Finalization.RecoveryRequired {
			t.Fatal("host did not settle confirmed job", progress, problem)
		}
		if _, err := os.Stat(filepath.Join(data, datahome.NewJobRecoveryPath)); !os.IsNotExist(err) {
			t.Fatal("host retained terminal journal", err)
		}
		if scenario == "diagnostic" {
			last, diagnosticErr := jobdiagnostics.ReadLastEvent(context.Background(), data, home)
			if diagnosticErr != nil || last == nil || last.RunID != progress.RunID || last.Operation != "intake" ||
				last.Event != "completed" || last.DurablePhase != "terminal" || progress.RecoveryRequired {
				t.Fatal("diagnostic coordinator did not close exact intake run", last, diagnosticErr, progress)
			}
		}
	} else {
		observed, recoveryErr := applicationwrite.New(data).PinnedToHome(home).ObserveNewJobRecovery(context.Background())
		wantLedger := map[string]string{"captured": "captured", "missing_schema": "database_missing",
			"assessment_unavailable": "captured", "assessment_success": "assessed"}[scenario]
		if recoveryErr != nil || observed == nil || observed.FileState != "complete" || observed.JobID != progress.Files.JobID ||
			observed.InitialStatus != "captured" || observed.LedgerState != wantLedger {
			t.Fatal("pending complete file intent was lost", observed, recoveryErr)
		}
	}
	switch scenario {
	case "missing_schema":
		if problem == nil || problem.Code != "SCHEMA_PREPARATION_REQUIRED" || progress.Ledger.Committed || progress.Ledger.CommitAttempted {
			t.Fatal("missing SQLite schema was reported as a captured job", progress, problem)
		}
		owner := applicationwrite.New(data).PinnedToHome(home)
		plan, planErr := owner.PlanIncompleteNewJobRecovery(context.Background())
		if planErr != nil || plan == nil || plan.Disposition != "schema_required" {
			t.Fatal("missing-schema recovery plan unavailable", plan, planErr)
		}
		recovery, recoveryErr := owner.RecoverIncompleteNewJobStep(context.Background(), plan.PlanID,
			start.ResourceRoot, assessmentcontext.Month{Year: 2026, Number: 9})
		if recoveryErr == nil || recoveryErr.Code != "SCHEMA_PREPARATION_REQUIRED" || recovery == nil || recovery.Step != "" {
			t.Fatal("recovery created a missing SQLite schema", recovery, recoveryErr)
		}
		if _, err := os.Stat(filepath.Join(data, "applications", "career_hub.sqlite3")); !os.IsNotExist(err) {
			t.Fatal("missing-schema case created a database")
		}
	case "captured":
		if problem != nil || !progress.Ledger.Committed || !progress.Ledger.CommitAttempted {
			t.Fatal("fresh captured registration failed", progress, problem)
		}
		db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var status string
		var events int
		if err := db.QueryRow("SELECT current_status FROM job_applications WHERE job_id=?", progress.Files.JobID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM application_status_events WHERE job_id=?", progress.Files.JobID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if status != "captured" || events != 1 {
			t.Fatal("captured registration was not exact", status, events)
		}
	case "assessment_unavailable":
		// This small host fixture deliberately lacks a complete assessment
		// Vault. The third phase must fail without hiding the captured commit.
		if problem == nil || !progress.Ledger.Committed || progress.Assessment == nil ||
			!progress.Assessment.RecoveryRequired || progress.Assessment.CommitAttempted || progress.Assessment.Committed {
			t.Fatal("post-Captured assessment failure hid committed facts", progress, problem)
		}
		db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var status string
		var events, assessments int
		if err := db.QueryRow("SELECT current_status FROM job_applications WHERE job_id=?", progress.Files.JobID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM application_status_events WHERE job_id=?", progress.Files.JobID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM job_assessments WHERE job_id=?", progress.Files.JobID).Scan(&assessments); err != nil {
			t.Fatal(err)
		}
		if status != "captured" || events != 1 || assessments != 0 {
			t.Fatal("partial assessment changed captured ledger", status, events, assessments)
		}
	case "assessment_success", "captured_finalized", "diagnostic":
		if problem != nil || !progress.Ledger.Committed || progress.Assessment == nil ||
			!progress.Assessment.Committed || !progress.Assessment.RecoveryRequired || progress.Applied != nil {
			t.Fatal("three-phase fresh job composition failed", progress, problem)
		}
		db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var status string
		var events, assessments int
		if err := db.QueryRow("SELECT current_status FROM job_applications WHERE job_id=?", progress.Files.JobID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM application_status_events WHERE job_id=?", progress.Files.JobID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM job_assessments WHERE job_id=?", progress.Files.JobID).Scan(&assessments); err != nil {
			t.Fatal(err)
		}
		if status != "captured" || events != 1 || assessments != 1 {
			t.Fatal("advisory assessment changed captured status", status, events, assessments)
		}
	case "applied":
		if problem != nil || !progress.Ledger.Committed || progress.Assessment == nil || !progress.Assessment.Committed ||
			progress.Applied == nil || !progress.Applied.Committed || !progress.Applied.RecoveryRequired {
			t.Fatal("explicit Applied host composition failed", progress, problem)
		}
		db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var status string
		var events, assessments, snapshots int
		if err := db.QueryRow("SELECT current_status FROM job_applications WHERE job_id=?", progress.Files.JobID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM application_status_events WHERE job_id=?", progress.Files.JobID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM job_assessments WHERE job_id=?", progress.Files.JobID).Scan(&assessments); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM application_applied_resume_events WHERE job_id=?", progress.Files.JobID).Scan(&snapshots); err != nil {
			t.Fatal(err)
		}
		if status != "applied" || events != 2 || assessments != 1 || snapshots != 1 {
			t.Fatal("explicit Applied effects not exact", status, events, assessments, snapshots)
		}
	default:
		t.Fatal("unknown fixture scenario")
	}
}

func runFreshJobIntakeHTTP(t *testing.T, start protocol.StartRecord, workspace *readonly.Workspace, request readonly.NewJobRequest) {
	t.Helper()
	start.SessionToken = testSessionToken
	h, err := preparedApplication(workspace, start).Handler(8766)
	if err != nil {
		t.Fatal(err)
	}
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	sessionRequest.Header.Set(httpapi.CapabilityHeader, testSessionToken)
	sessionRequest.Host = "127.0.0.1:8766"
	sessionResponse := httptest.NewRecorder()
	h.ServeHTTP(sessionResponse, sessionRequest)
	var session struct {
		Token string `json:"token"`
	}
	session.Token = testSessionToken // the response no longer carries it
	if sessionResponse.Code != http.StatusOK || json.Unmarshal(sessionResponse.Body.Bytes(), &session) != nil || session.Token == "" {
		t.Fatalf("native intake session unavailable: %d %.300s", sessionResponse.Code, sessionResponse.Body.String())
	}
	body, _ := json.Marshal(map[string]string{
		"source_text": request.RawText, "source_name": request.SourceName,
		"company": "Fictional Labs", "title": "Backend Engineer",
		"source_url": request.SourceURL, "idempotency_key": request.IdempotencyKey,
		"initial_status": "captured",
	})
	post := func(payload []byte, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/jobs", bytes.NewReader(payload))
		r.Host = "127.0.0.1:8766"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(httpapi.CapabilityHeader, session.Token)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		var result map[string]any
		if response.Code != want || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("native intake response failed: %d %.500s", response.Code, response.Body.String())
		}
		return result
	}
	fresh := post(body, http.StatusCreated)
	duplicate := post(body, http.StatusOK)
	for _, item := range []struct {
		value         map[string]any
		wantDuplicate bool
	}{{fresh, false}, {duplicate, true}} {
		job, ok := item.value["job"].(map[string]any)
		application, applicationOK := job["application"].(map[string]any)
		assessment, assessmentOK := job["assessment"].(map[string]any)
		runID, runOK := item.value["run_id"].(string)
		if !ok || job["id"] != "fictional_job" || !applicationOK || application["current_status"] != "captured" ||
			!assessmentOK || assessment["assessment_mode"] != "advisory_only" || item.value["duplicate"] != item.wantDuplicate ||
			!runOK || !jobdiagnostics.ValidRunID(runID) {
			t.Fatal("native intake response was not source-shaped", item.value)
		}
	}
	if fresh["run_id"] == duplicate["run_id"] {
		t.Fatal("duplicate request reused a diagnostic run")
	}
	var changed map[string]string
	if json.Unmarshal(body, &changed) != nil {
		t.Fatal("fictional intake body changed")
	}
	changed["source_text"] += "\nChanged source under the same retry identity."
	conflictBody, _ := json.Marshal(changed)
	conflict := post(conflictBody, http.StatusConflict)
	if conflict["error_code"] != "JOB_INTAKE_RETRY_CONFLICT" || conflict["diagnostic_category"] != "job_intake" {
		t.Fatal("retry conflict was not classified", conflict)
	}
}

func runRecoveredJobIntake(t *testing.T, data string, start protocol.StartRecord, workspace *readonly.Workspace,
	request readonly.NewJobRequest, applied bool) {
	t.Helper()
	if applied {
		request.InitialStatus = "applied"
	}
	files, fileErr := beginNewJobFiles(context.Background(), workspace, start, request,
		"Fictional Labs", "Backend Engineer")
	if fileErr != nil || files == nil || !files.Complete || !files.RecoveryRequired {
		t.Fatal("recovery fixture did not stop after complete files", files, fileErr)
	}
	home, homeErr := workspace.StartupHome()
	if homeErr != nil {
		t.Fatal(homeErr)
	}
	owner := applicationwrite.New(data).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
	month := assessmentcontext.Month{Year: 2026, Number: 9}
	wantSteps := []string{"ledger", "assessment", "terminal"}
	if applied {
		wantSteps = []string{"ledger", "assessment", "applied", "terminal"}
	}
	var lastPlanID string
	for _, want := range wantSteps {
		plan, planErr := owner.PlanIncompleteNewJobRecovery(context.Background())
		if planErr != nil || plan == nil || plan.NextStep != want || !plan.CanAdvance {
			t.Fatal("explicit recovery plan failed", want, plan, planErr)
		}
		step, stepErr := owner.RecoverIncompleteNewJobStep(context.Background(), plan.PlanID, start.ResourceRoot, month)
		if stepErr != nil || step == nil || step.Step != want || step.Observation == nil ||
			step.Plan == nil || step.Plan.PlanID != plan.PlanID {
			t.Fatal("explicit recovery step failed", want, step, stepErr)
		}
		lastPlanID = plan.PlanID
		switch want {
		case "ledger":
			if step.Observation.LedgerState != "job_missing" || step.Ledger == nil || !step.Ledger.Committed {
				t.Fatal("recovery did not register exactly one missing job", step)
			}
			stale, staleErr := owner.RecoverIncompleteNewJobStep(context.Background(), plan.PlanID, start.ResourceRoot, month)
			if staleErr == nil || staleErr.Code != "JOB_INTAKE_RECOVERY_PLAN_STALE" || stale == nil ||
				stale.Step != "" || stale.Plan == nil || stale.Plan.PlanID == plan.PlanID {
				t.Fatal("completed plan revision was reused", stale, staleErr)
			}
		case "assessment":
			if step.Observation.LedgerState != "captured" || step.Assessment == nil || !step.Assessment.Committed {
				t.Fatal("recovery did not assess the exact captured job", step)
			}
		case "applied":
			if step.Observation.LedgerState != "assessed" || step.Applied == nil || !step.Applied.Committed {
				t.Fatal("recovery did not apply the explicit sealed status", step)
			}
		case "terminal":
			if step.Terminal == nil || !step.Terminal.RemovalObserved || step.Terminal.RecoveryRequired {
				t.Fatal("recovery did not settle the terminal journal", step)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(data, datahome.NewJobRecoveryPath)); !os.IsNotExist(err) {
		t.Fatal("completed recovery retained the pending journal", err)
	}
	if repeated, repeatErr := owner.RecoverIncompleteNewJobStep(context.Background(), lastPlanID, start.ResourceRoot, month); repeatErr == nil ||
		repeatErr.Code != "JOB_INTAKE_RECOVERY_INTENT_MISSING" || repeated == nil || repeated.Step != "" {
		t.Fatal("completed recovery was replayed", repeated, repeatErr)
	}
	db, dbErr := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
	if dbErr != nil {
		t.Fatal(dbErr)
	}
	defer db.Close()
	var status string
	var jobs, events, assessments, snapshots int
	for _, item := range []struct {
		target *int
		query  string
	}{{&jobs, "SELECT COUNT(*) FROM job_applications"}, {&events, "SELECT COUNT(*) FROM application_status_events"},
		{&assessments, "SELECT COUNT(*) FROM job_assessments"}, {&snapshots, "SELECT COUNT(*) FROM application_applied_resume_events"}} {
		if err := db.QueryRow(item.query).Scan(item.target); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow("SELECT current_status FROM job_applications WHERE job_id='fictional_job'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	wantStatus, wantEvents, wantSnapshots := "captured", 1, 0
	if applied {
		wantStatus, wantEvents, wantSnapshots = "applied", 2, 1
	}
	if jobs != 1 || events != wantEvents || assessments != 1 || snapshots != wantSnapshots || status != wantStatus {
		t.Fatal("explicit recovery duplicated or omitted effects", jobs, events, assessments, snapshots, status)
	}
}

func runDuplicateJobIntake(t *testing.T, data string, start protocol.StartRecord, workspace *readonly.Workspace,
	request readonly.NewJobRequest) {
	t.Helper()
	month := assessmentcontext.Month{Year: 2026, Number: 9}
	first, firstErr := intakeFreshNewJob(context.Background(), workspace, start, request,
		"Fictional Labs", "Backend Engineer", month)
	if firstErr != nil || first == nil || first.Finalization == nil || first.Finalization.RecoveryRequired {
		t.Fatal("fresh prerequisite did not finalize", first, firstErr)
	}
	rawPath := filepath.Join(data, "jobs", "incoming", "fictional_job.txt")
	cardPath := filepath.Join(data, "jobs", "normalized", "fictional_job.yaml")
	rawBefore, rawErr := os.ReadFile(rawPath)
	cardBefore, cardErr := os.ReadFile(cardPath)
	if rawErr != nil || cardErr != nil {
		t.Fatal("fresh duplicate fixture unavailable", rawErr, cardErr)
	}
	captured, capturedErr := intakeFreshNewJob(context.Background(), workspace, start, request,
		"Fictional Labs", "Backend Engineer", month)
	if capturedErr != nil || captured == nil || captured.Files == nil || captured.Files.Duplicate == nil ||
		captured.Duplicate == nil || captured.Duplicate.CommitAttempted || captured.Ledger != nil || captured.Finalization != nil {
		t.Fatal("captured duplicate was not a no-file, cached transaction", captured, capturedErr)
	}
	home, homeErr := workspace.StartupHome()
	if homeErr != nil {
		t.Fatal(homeErr)
	}
	owner := applicationwrite.New(data).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
	reused, reusedErr := owner.ProcessExistingJobDuplicate(context.Background(), start.ResourceRoot, month, captured.Files)
	if reusedErr == nil || reusedErr.Code != "JOB_INTAKE_DUPLICATE_ALREADY_ATTEMPTED" || reused == nil || reused.CommitAttempted {
		t.Fatal("duplicate receipt allowed a blind retry", reused, reusedErr)
	}
	driftPhase, driftPhaseErr := beginNewJobFiles(context.Background(), workspace, start, request,
		"Fictional Labs", "Backend Engineer")
	if driftPhaseErr != nil || driftPhase == nil || driftPhase.Duplicate == nil {
		t.Fatal("duplicate drift receipt unavailable", driftPhase, driftPhaseErr)
	}
	if err := os.WriteFile(rawPath, append(bytes.Clone(rawBefore), []byte("\nchanged")...), 0600); err != nil {
		t.Fatal(err)
	}
	drifted, driftErr := owner.ProcessExistingJobDuplicate(context.Background(), start.ResourceRoot, month, driftPhase)
	if driftErr == nil || driftErr.Code != "JOB_INTAKE_DUPLICATE_CHANGED" || drifted == nil || drifted.CommitAttempted {
		t.Fatal("changed duplicate source reached SQLite", drifted, driftErr)
	}
	if err := os.WriteFile(rawPath, rawBefore, 0600); err != nil {
		t.Fatal(err)
	}
	appliedRequest := request
	appliedRequest.InitialStatus = "applied"
	applied, appliedErr := intakeFreshNewJob(context.Background(), workspace, start, appliedRequest,
		"Fictional Labs", "Backend Engineer", month)
	if appliedErr != nil || applied == nil || applied.Files == nil || applied.Files.Duplicate == nil ||
		applied.Duplicate == nil || !applied.Duplicate.Committed || applied.Ledger != nil || applied.Finalization != nil {
		t.Fatal("explicit Applied duplicate did not update one existing ledger", applied, appliedErr)
	}
	rawAfter, rawErr := os.ReadFile(rawPath)
	cardAfter, cardErr := os.ReadFile(cardPath)
	if rawErr != nil || cardErr != nil || !bytes.Equal(rawBefore, rawAfter) || !bytes.Equal(cardBefore, cardAfter) {
		t.Fatal("duplicate intake rewrote JD files", rawErr, cardErr)
	}
	db, dbErr := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
	if dbErr != nil {
		t.Fatal(dbErr)
	}
	defer db.Close()
	var jobs, events, assessments, snapshots int
	var status string
	for _, item := range []struct {
		target *int
		query  string
	}{{&jobs, "SELECT COUNT(*) FROM job_applications"}, {&events, "SELECT COUNT(*) FROM application_status_events"},
		{&assessments, "SELECT COUNT(*) FROM job_assessments"}, {&snapshots, "SELECT COUNT(*) FROM application_applied_resume_events"}} {
		if err := db.QueryRow(item.query).Scan(item.target); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow("SELECT current_status FROM job_applications WHERE job_id='fictional_job'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || events != 2 || assessments != 1 || snapshots != 1 || status != "applied" {
		t.Fatal("duplicate intake created duplicate effects", jobs, events, assessments, snapshots, status)
	}
	if _, err := os.Stat(filepath.Join(data, datahome.NewJobRecoveryPath)); !os.IsNotExist(err) {
		t.Fatal("duplicate intake created a pending journal", err)
	}
}
