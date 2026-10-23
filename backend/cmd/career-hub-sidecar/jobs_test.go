package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func TestResumeManagementMappingIsOperationSpecific(t *testing.T) {
	for _, tc := range []struct{ owner, code, category string }{
		{"APPLICATION_INPUT_LIMIT", "RESUME_MANAGEMENT_INVALID", "input"},
		{"RESUME_MANAGEMENT_REPORT_LIMIT", "RESUME_MANAGEMENT_LIMIT", "limit"},
		{"APPLICATION_READ_CANCELED", "RESUME_MANAGEMENT_CANCELED", "canceled"},
		{"RESUME_MANAGEMENT_DATABASE_CHANGED", "RESUME_MANAGEMENT_CHANGED", "drift"},
		{"RESUME_MANAGEMENT_CATALOG_CHANGED", "RESUME_MANAGEMENT_CHANGED", "drift"},
		{"RESUME_MANAGEMENT_RUNS_CHANGED", "RESUME_MANAGEMENT_CHANGED", "drift"},
		{"RESUME_MANAGEMENT_REPORT_REJECTED", "RESUME_MANAGEMENT_UNSAFE", "unsafe"},
		{"APPLICATION_PATH_REJECTED", "RESUME_MANAGEMENT_UNSAFE", "unsafe"},
		{"RESUME_MANAGEMENT_CLEANUP_FAILED", "RESUME_MANAGEMENT_CLEANUP_FAILED", "cleanup"},
		{"APPLICATION_DATABASE_UNAVAILABLE", "RESUME_MANAGEMENT_UNAVAILABLE", "storage"},
		{"APPLICATION_SCHEMA_TOO_NEW", "RESUME_MANAGEMENT_UNAVAILABLE", "storage"},
		{"APPLICATION_SCHEMA_PREPARATION_REQUIRED", "RESUME_MANAGEMENT_UNAVAILABLE", "storage"},
		{"APPLICATION_VALUE_UNSUPPORTED", "RESUME_MANAGEMENT_UNAVAILABLE", "storage"},
		{"private-error", "RESUME_MANAGEMENT_READ_FAILED", "read"},
	} {
		out, err := resumeManagementResult(nil, &readonly.Error{Code: tc.owner})
		if out == nil || len(out.Result) != 0 || err == nil || err.Code != tc.code || err.Category != tc.category {
			t.Fatalf("%s: %#v %#v", tc.owner, out, err)
		}
	}
	_, err := resumeManagementResult(nil, context.DeadlineExceeded)
	if err.Code != "RESUME_MANAGEMENT_CANCELED" {
		t.Fatal(err)
	}
	active, err := jobReadResult(nil, &readonly.Error{Code: "RESUME_MANAGEMENT_REPORT_REJECTED"})
	if active == nil || err.Code != "JOB_LIBRARY_UNAVAILABLE" {
		t.Fatal("saved-job allowlist widened")
	}
	out, err := resumeManagementResult(nil, nil)
	if err != nil || !json.Valid(out.Result) || string(out.Result) != `{"resumes":[]}` {
		t.Fatal("invalid management envelope", out, err)
	}
}

func TestResumeDigestMappingPreservesNativePublicationFacts(t *testing.T) {
	resumeID := "resume-one"
	good := &applicationwrite.Outcome{IntakeReportPublished: true, IntakeState: "published", Result: json.RawMessage(
		`{"resume_id":"resume-one","source_id":"src_resume_one_digest_a","technology_count":2,"bullet_count":3}`)}
	out, problem := resumeDigestResult(resumeID, good, nil)
	if problem != nil || out == nil || out.Partial || string(out.Result) != string(good.Result) {
		t.Fatal("valid digest result was not projected exactly", out, problem)
	}
	for _, invalid := range []*applicationwrite.Outcome{
		nil,
		{IntakeReportPublished: false, IntakeState: "published", Result: good.Result},
		{IntakeReportPublished: true, IntakeState: "recovery_required", RecoveryRequired: true, Result: good.Result},
		{IntakeReportPublished: true, IntakeState: "published", Result: json.RawMessage(`{"resume_id":"other","source_id":"src_resume_one_digest_a","technology_count":2,"bullet_count":3}`)},
		{IntakeReportPublished: true, IntakeState: "published", Result: json.RawMessage(`{"resume_id":"resume-one","source_id":"../private","technology_count":2,"bullet_count":3}`)},
	} {
		failed, failure := resumeDigestResult(resumeID, invalid, nil)
		if failure == nil || failure.Code != "RESUME_DIGEST_FAILED" || failed == nil || len(failed.Result) != 0 {
			t.Fatal("invalid digest response escaped", invalid, failed, failure)
		}
	}
	for _, tc := range []struct{ owner, public string }{
		{"INTAKE_RESUME_ID_INVALID", "RESUME_DIGEST_REQUEST_INVALID"},
		{"INTAKE_RESUME_NOT_FOUND", "RESUME_DIGEST_NOT_FOUND"},
		{"INTAKE_RESUME_ARCHIVED", "RESUME_DIGEST_ARCHIVED"},
		{"INTAKE_FORMAT_UNSUPPORTED", "RESUME_DIGEST_UNSUPPORTED"},
		{"INTAKE_SOURCE_UNAVAILABLE", "RESUME_DIGEST_SOURCE_INVALID"},
		{"INTAKE_WITNESS_CHANGED", "RESUME_DIGEST_INPUT_CHANGED"},
		{"INTAKE_REPORT_LIMIT", "RESUME_DIGEST_LIMIT"},
		{"INTAKE_CANCELED", "RESUME_DIGEST_CANCELED"},
		{"PATH_LINK", "RESUME_DIGEST_UNSAFE"},
		{"INTAKE_DATABASE_UNAVAILABLE", "RESUME_DIGEST_UNAVAILABLE"},
		{"private", "RESUME_DIGEST_FAILED"},
	} {
		mapped, failure := resumeDigestResult(resumeID, &applicationwrite.Outcome{}, &applicationwrite.Error{Code: tc.owner, SourceMessage: "private path"})
		if mapped == nil || failure == nil || failure.Code != tc.public || failure.Category != "resume_digest" {
			t.Fatal("unsafe digest error mapping", tc, mapped, failure)
		}
	}
	recovery, failure := resumeDigestResult(resumeID, &applicationwrite.Outcome{RecoveryRequired: true, IntakeState: "recovery_required"}, &applicationwrite.Error{Code: "private"})
	if failure.Code != "RESUME_DIGEST_RECOVERY_REQUIRED" || !recovery.RecoveryRequired || !recovery.Partial {
		t.Fatal("recovery state was hidden", recovery, failure)
	}
	unconfirmed, failure := resumeDigestResult(resumeID, &applicationwrite.Outcome{IntakeReportPublished: true, IntakeState: "published_unconfirmed"}, &applicationwrite.Error{Code: "private"})
	if failure.Code != "RESUME_DIGEST_COMPLETED_UNCONFIRMED" || !unconfirmed.CommitUnknown || !unconfirmed.Partial || unconfirmed.RecoveryRequired {
		t.Fatal("completed-unconfirmed state was flattened", unconfirmed, failure)
	}
}

func TestJobEditorErrorMappingIsSafeAndRetainsPublication(t *testing.T) {
	for _, test := range []struct {
		stage, code, class, expected string
		plan                         bool
	}{
		{"contact", "", "ApplicationContactNotFound", "CONTACT_NOT_FOUND", false},
		{"contact", "", "ApplicationContactRevisionConflict", "CONTACT_CONFLICT", false},
		{"contact", "", "ApplicationContactArchived", "CONTACT_CONFLICT", false},
		{"contact", "", "ApplicationContactError", "CONTACT_REQUEST_INVALID", false},
		{"database", "private", "", "CONTACT_OPERATION_FAILED", false},
		{"applications", "NOT_FOUND", "", "APPLICATION_JOB_NOT_FOUND", true},
		{"validation", "", "ResumePlanError", "RESUME_PLAN_INVALID", true},
		{"revision", "", "ApplicationRevisionConflict", "RESUME_PLAN_INVALID", true},
		{"database", "private", "", "RESUME_PLAN_SAVE_FAILED", true},
	} {
		for _, committed := range []bool{false, true} {
			value := &applicationwrite.Outcome{Committed: committed, CommitAttempted: true, RecoveryRequired: true, CleanupFailed: true}
			out, problem := jobEditorResult(value, &applicationwrite.Error{Stage: test.stage, Code: test.code, SourceClass: test.class, SourceMessage: "private diagnostic"}, test.plan)
			if problem == nil || problem.Code != test.expected || out.Committed != committed || out.CommitUnknown == committed || !out.CleanupFailed || !out.RecoveryRequired {
				t.Fatal("unsafe or incorrect editor error mapping", test.expected)
			}
		}
	}
}

func TestJobCompositionKeepsEarlierCommitThroughLateFailure(t *testing.T) {
	for _, next := range []*httpapi.ApplicationOutcome{nil, {CommitUnknown: true, RecoveryRequired: true, CleanupFailed: true}} {
		out, e := mergeJobResult(&httpapi.ApplicationOutcome{Committed: true}, next, &httpapi.ApplicationError{Code: "JOB_OPERATION_FAILED"})
		if e == nil || !out.Committed || !out.Partial || out.Result != nil {
			t.Fatal("earlier commit lost")
		}
		if next != nil && (!out.CommitUnknown || !out.RecoveryRequired || !out.CleanupFailed) {
			t.Fatal("later uncertainty lost")
		}
	}
}

func TestJobDetailsMappingPreservesRevisionAndPublicationFacts(t *testing.T) {
	for _, tc := range []struct{ owner, public string }{
		{"JOB_DETAILS_REQUEST_INVALID", "JOB_DETAILS_REQUEST_INVALID"},
		{"JOB_DETAILS_INVALID", "JOB_DETAILS_REQUEST_INVALID"},
		{"JOB_CARD_NOT_FOUND", "JOB_CARD_NOT_FOUND"},
		{"JOB_DETAILS_CARD_INVALID", "JOB_CARD_INVALID"},
		{"JOB_DETAILS_SOURCE_CHANGED", "JOB_DETAILS_REVISION_CONFLICT"},
		{"JOB_DETAILS_REVISION_CONFLICT", "JOB_DETAILS_REVISION_CONFLICT"},
		{"DATA_HOME_OWNERSHIP_REQUIRED", "JOB_DETAILS_UNAVAILABLE"},
		{"JOB_DETAILS_WRITE_UNCERTAIN", "JOB_DETAILS_UNAVAILABLE"},
	} {
		out, problem := jobDetailsWriteResult(&applicationwrite.Outcome{}, &applicationwrite.Error{Code: tc.owner, SourceMessage: "private path"})
		if out == nil || problem == nil || problem.Code != tc.public || out.Committed || out.CommitUnknown {
			t.Fatal("incorrect precommit detail mapping", tc, out, problem)
		}
	}
	for _, state := range []applicationwrite.Outcome{{CommitAttempted: true}, {CommitAttempted: true, Committed: true, CleanupFailed: true}} {
		out, problem := jobDetailsWriteResult(&state, &applicationwrite.Error{Code: "JOB_DETAILS_COMMITTED_UNVERIFIED"})
		if problem == nil || problem.Code != "JOB_DETAILS_SAVE_UNVERIFIED" || out.Committed != state.Committed || out.CommitUnknown == state.Committed || out.CleanupFailed != state.CleanupFailed {
			t.Fatal("uncertain or committed detail result was hidden", out, problem)
		}
	}
}

func TestResumeMutationErrorsKeepSafeClassAndPublicationFacts(t *testing.T) {
	for _, tc := range []struct{ operation, class, code, expected string }{
		{"resume.upload", "ApplicationHistoryError", "", "RESUME_LIBRARY_UPLOAD_INVALID"},
		{"resume.upload", "", "RESUME_LIBRARY_INPUT_LIMIT", "RESUME_LIBRARY_UPLOAD_INVALID"},
		{"resume.upload", "", "RESUME_LIBRARY_IDENTITY_COLLISION", "RESUME_VERSION_IDENTITY_COLLISION"},
		{"resume.upload", "", "private", "RESUME_LIBRARY_UPLOAD_FAILED"},
		{"resume.attach", "ApplicationNotFound", "", "APPLICATION_JOB_NOT_FOUND"},
		{"resume.attach", "ApplicationHistoryError", "", "APPLICATION_RESUME_INVALID"},
		{"resume.attach", "", "RESUME_LIBRARY_INPUT_LIMIT", "APPLICATION_RESUME_INVALID"},
		{"resume.attach", "", "RESUME_LIBRARY_IDENTITY_COLLISION", "RESUME_VERSION_IDENTITY_COLLISION"},
		{"resume.attach", "", "private", "APPLICATION_RESUME_SAVE_FAILED"},
		{"resume.delete", "ResumeNotFound", "", "RESUME_LIBRARY_NOT_FOUND"},
		{"resume.delete", "ResumeVersionInUse", "", "RESUME_VERSION_IN_USE"},
		{"resume.delete", "ResumeVersionFileMissing", "", "RESUME_VERSION_FILE_MISSING"},
		{"resume.delete", "ResumeVersionFileInvalid", "", "RESUME_VERSION_FILE_INVALID"},
		{"resume.delete", "ApplicationHistoryError", "", "RESUME_VERSION_DELETE_INVALID"},
		{"resume.delete", "", "private", "RESUME_VERSION_DELETE_FAILED"},
	} {
		value := &applicationwrite.ResumeOutcome{Outcome: &applicationwrite.Outcome{
			CommitAttempted: true, RecoveryRequired: true, CleanupFailed: true,
		}}
		out, problem := resumeJobResult(tc.operation, value, &applicationwrite.Error{
			Code: tc.code, SourceClass: tc.class, SourceMessage: "private file path and data",
		})
		if problem == nil || problem.Code != tc.expected || out == nil || !out.CommitUnknown ||
			!out.RecoveryRequired || !out.CleanupFailed || out.Committed {
			t.Fatal("unsafe resume mutation mapping", tc)
		}
	}
}

func TestApplicationResumeRegistrationErrorsUseSourceCodes(t *testing.T) {
	for _, tc := range []struct{ input, expected string }{
		{"JOB_CARD_NOT_FOUND", "APPLICATION_JOB_NOT_FOUND"},
		{"JOB_CARD_INVALID", "APPLICATION_RESUME_INVALID"},
		{"JOB_LIBRARY_CHANGED", "APPLICATION_RESUME_SAVE_FAILED"},
		{"LOCAL_OPERATION_CANCELED", "LOCAL_OPERATION_CANCELED"},
	} {
		problem := applicationResumeError(&httpapi.ApplicationError{Code: tc.input, Category: "synthetic"})
		if problem == nil || problem.Code != tc.expected || problem.Category != "synthetic" {
			t.Fatal("application resume registration mapping changed", tc, problem)
		}
	}
}

func TestSubmittedResumeFileErrorsKeepSourceClassification(t *testing.T) {
	for _, operation := range []string{"resume.submitted_file", "resume.attachment_file"} {
		for _, tc := range []struct{ class, code, expected string }{
			{"ApplicationNotFound", "", "APPLICATION_SUBMITTED_RESUME_NOT_FOUND"},
			{"ApplicationHistoryError", "", "APPLICATION_SUBMITTED_RESUME_INTEGRITY_FAILED"},
			{"", "RESUME_LIBRARY_FILE_UNAVAILABLE", "APPLICATION_SUBMITTED_RESUME_READ_FAILED"},
			{"", "RESUME_LIBRARY_CANCELED", "LOCAL_OPERATION_CANCELED"},
		} {
			out, problem := resumeJobResult(operation, &applicationwrite.ResumeOutcome{Outcome: &applicationwrite.Outcome{}}, &applicationwrite.Error{
				SourceClass: tc.class, Code: tc.code, SourceMessage: "private path",
			})
			expected := tc.expected
			if operation == "resume.attachment_file" {
				expected = strings.Replace(expected, "APPLICATION_SUBMITTED_RESUME_", "APPLICATION_RESUME_", 1)
			}
			if out == nil || problem == nil || problem.Code != expected {
				t.Fatal("submitted-file error mapping changed", tc, out, problem)
			}
		}
	}
}
