package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationchecklist"
	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/intake/resumelibrary"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/savedassessment"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func jobActions(w *readonly.Workspace, start protocol.StartRecord, reanalysis *reanalysisCoordinator) func(context.Context, httpapi.JobRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	owner := applicationwrite.New(start.DataRoot)
	return func(ctx context.Context, r httpapi.JobRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		if r.Operation == "contact.candidate_preview" || r.Operation == "contact.candidate_confirm" {
			return contactCandidateAction(ctx, owner, r)
		}
		if strings.HasPrefix(r.Operation, "contact.") {
			return contactAction(ctx, owner, r)
		}
		if r.Operation == "plan.load" || r.Operation == "plan.save" {
			if r.Plan == nil || r.Plan.JobID != r.ID {
				return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "RESUME_PLAN_INVALID"}
			}
			// Unlike status/checklist preparation, the editor requires an
			// existing ledger row; it must not implicitly register a job.
			value, err := owner.ResumePlan(ctx, *r.Plan)
			return jobEditorResult(value, err, true)
		}
		if r.Operation == "resume.file" || r.Operation == "resume.submitted_file" || r.Operation == "resume.attachment_file" || r.Operation == "resume.archive" || r.Operation == "resume.restore" || r.Operation == "resume.upload" || r.Operation == "resume.delete" {
			operation := "version_file"
			fields := map[string]any{"resume_id": r.ID, "require_active": false}
			switch r.Operation {
			case "resume.attachment_file":
				operation = "attachment_file"
				fields = map[string]any{"job_id": r.ID}
			case "resume.submitted_file":
				operation = "submitted_file"
				fields = map[string]any{"job_id": r.ID}
			case "resume.archive", "resume.restore":
				operation = "archive"
				fields = map[string]any{"resume_id": r.ID, "archived": r.Operation == "resume.archive"}
			case "resume.upload":
				operation = "upload"
				fields = r.Fields
			case "resume.delete":
				operation = "delete"
				fields = map[string]any{"resume_id": r.ID}
			}
			result, err := owner.Resume(ctx, resumelibrary.Request{Operation: operation, Fields: fields, Content: r.Content})
			out, problem := resumeJobResult(r.Operation, result, err)
			if problem != nil {
				return out, problem
			}
			if r.Operation == "resume.file" || r.Operation == "resume.submitted_file" || r.Operation == "resume.attachment_file" {
				if result == nil || !result.IsFile || len(result.Bytes) == 0 {
					return out, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID"}
				}
				out.Content = result.Bytes
			}
			return out, nil
		}
		if r.Operation == "resume.digest" {
			home, homeErr := w.StartupHome()
			if homeErr != nil {
				return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "RESUME_DIGEST_UNAVAILABLE", Category: "resume_digest"}
			}
			value, digestErr := owner.PinnedToHome(home).DigestResumeVersion(ctx, start.ResourceRoot, r.ID)
			return resumeDigestResult(r.ID, value, digestErr)
		}
		if r.Operation == "resume.list" {
			if r.Fields["include_archived"] == true {
				rows, err := w.ResumeManagement(ctx, applications.ResumeFilters{Search: r.Fields["search"].(string), IncludeArchived: true})
				return resumeManagementResult(rows, err)
			}
			result, e := owner.Resume(ctx, resumelibrary.Request{Operation: "list", Fields: r.Fields})
			var value *applicationwrite.Outcome
			if result != nil {
				value = result.Outcome
			}
			out, problem := jobWriteResult(value, e)
			if problem != nil {
				return out, problem
			}
			wrapped, problem := jobReadResult(map[string]any{"resumes": out.Result}, nil)
			return mergeJobResult(out, wrapped, problem)
		}
		if r.Operation == "list" {
			value, err := w.SavedJobs(ctx, r.Offset, r.Limit)
			return jobReadResult(value, err)
		}
		if r.Operation == "detail" {
			value, err := w.SavedJob(ctx, r.ID)
			if err != nil {
				return jobReadResult(nil, err)
			}
			return jobReadResult(map[string]any{"job": value.Card}, nil)
		}
		if r.Operation == "details.edit" {
			home, homeErr := w.StartupHome()
			if homeErr != nil {
				return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "JOB_DETAILS_UNAVAILABLE"}
			}
			value, editErr := owner.PinnedToHome(home).EditJobDetails(ctx, r.ID, r.ExpectedCardRevision, r.Fields)
			out, problem := jobDetailsWriteResult(value, editErr)
			if problem != nil {
				return out, problem
			}
			if out == nil || !out.Committed || value == nil {
				return mergeJobResult(out, nil, &httpapi.ApplicationError{Code: "JOB_DETAILS_SAVE_UNVERIFIED"})
			}
			var committed struct {
				ID           string `json:"id"`
				CardRevision string `json:"card_revision"`
			}
			if json.Unmarshal(value.Result, &committed) != nil || committed.ID != r.ID || committed.CardRevision == "" {
				return mergeJobResult(out, nil, &httpapi.ApplicationError{Code: "JOB_DETAILS_SAVE_UNVERIFIED"})
			}
			// A committed edit has its own publication fact. If the separate UI
			// read fails, report an unverified response, never a rolled-back edit.
			saved, readErr := w.SavedJob(ctx, r.ID)
			if readErr != nil || saved == nil || saved.Card["card_revision"] != committed.CardRevision {
				return mergeJobResult(out, nil, &httpapi.ApplicationError{Code: "JOB_DETAILS_SAVE_UNVERIFIED"})
			}
			wrapped, e := jobReadResult(map[string]any{"job": saved.Card}, nil)
			if e != nil {
				return mergeJobResult(out, wrapped, &httpapi.ApplicationError{Code: "JOB_DETAILS_SAVE_UNVERIFIED"})
			}
			return mergeJobResult(out, wrapped, nil)
		}
		if r.Operation == "reanalyze" {
			return reanalysis.Reanalyze(ctx, r.ID, r.ExpectedCardRevision)
		}
		if r.Operation == "source" {
			value, err := w.SavedJobSource(ctx, r.ID)
			return jobReadResult(value, err)
		}
		if r.Operation == "assessment" || r.Operation == "application" || r.Operation == "assessment.resolve" {
			now := time.Now().UTC()
			operation := "load"
			fields := map[string]any{"job_id": r.ID}
			if r.Operation == "assessment.resolve" {
				operation = "resolve"
				for key, value := range r.Fields {
					fields[key] = value
				}
			}
			value, err := owner.AssessSaved(ctx, start.ResourceRoot, savedassessment.Request{Operation: operation, Fields: fields, Month: assessmentcontext.Month{Year: now.Year(), Number: int(now.Month())}})
			out, problem := jobWriteResult(value, err)
			if problem != nil {
				return out, problem
			}
			var payload map[string]json.RawMessage
			if out == nil || json.Unmarshal(out.Result, &payload) != nil || len(payload["assessment"]) == 0 {
				return mergeJobResult(out, nil, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID"})
			}
			if r.Operation != "application" {
				out.Result = payload["assessment"]
				return out, nil
			}
			detail, failure := w.Application(ctx, r.ID)
			read, problem := jobReadResult(detail, failure)
			if problem != nil {
				return mergeJobResult(out, read, problem)
			}
			var application map[string]json.RawMessage
			if json.Unmarshal(read.Result, &application) != nil {
				return mergeJobResult(out, nil, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID"})
			}
			application["assessment"] = payload["assessment"]
			read, problem = jobReadResult(application, nil)
			return mergeJobResult(out, read, problem)
		}
		var prior *httpapi.ApplicationOutcome
		// Source handlers register an existing valid saved card before these
		// actions. Registration is Captured only; never Applied or Preparing.
		if r.Operation == "decision" || r.Operation == "status" || r.Operation == "start" || r.Operation == "resume.attach" || r.Operation == "resume.select" || r.Operation == "session.latest" || r.Operation == "session.create" || strings.HasPrefix(r.Operation, "checklist.") {
			job, err := w.SavedJob(ctx, r.ID)
			if err != nil {
				out, problem := jobReadResult(nil, err)
				if r.Operation == "resume.attach" {
					problem = applicationResumeError(problem)
				}
				return out, problem
			}
			value, writeErr := owner.RegisterJob(ctx, job.Card)
			var problem *httpapi.ApplicationError
			prior, problem = jobWriteResult(value, writeErr)
			if problem != nil {
				if r.Operation == "resume.attach" {
					problem = applicationResumeError(problem)
				}
				return prior, problem
			}
		}
		if r.Operation == "resume.attach" {
			result, err := owner.Resume(ctx, resumelibrary.Request{Operation: "attach", Fields: r.Fields, Content: r.Content})
			out, problem := resumeJobResult(r.Operation, result, err)
			return mergeJobResult(prior, out, problem)
		}
		var value *applicationwrite.Outcome
		var err *applicationwrite.Error
		switch r.Operation {
		case "resume.select", "resume.submitted":
			fields := map[string]any{"job_id": r.ID}
			for key, value := range r.Fields {
				fields[key] = value
			}
			result, e := owner.Resume(ctx, resumelibrary.Request{Operation: strings.TrimPrefix(r.Operation, "resume."), Fields: fields})
			err = e
			if result != nil {
				value = result.Outcome
			}
		case "decision":
			value, err = owner.SetDecision(ctx, r.ID, stringField(r.Fields, "decision"), r.Fields["notes"], r.Fields["expected_revision"])
		case "status":
			note := r.Fields["note"]
			if note == nil {
				note = ""
			}
			value, err = owner.SetStatus(ctx, r.ID, stringField(r.Fields, "status"), optionalTimestamp(r.Fields["occurred_at"]), note, r.Fields["expected_revision"])
		case "start":
			value, err = owner.StartApplication(ctx, r.ID, optionalTimestamp(r.Fields["occurred_at"]))
		case "session.latest":
			value, err = owner.LatestApplicationSession(ctx, r.ID)
		case "session.create":
			value, err = owner.CreateApplicationSession(ctx, r.ID)
		case "session.reviews":
			value, err = owner.SubmissionReviews(ctx)
		case "session.confirm":
			value, err = owner.ConfirmApplicationSession(ctx, r.ID, r.Fields["expected_revision"])
		case "session.dismiss":
			value, err = owner.DismissApplicationSession(ctx, r.ID, r.Fields["expected_revision"])
		case "session.cancel":
			value, err = owner.CancelApplicationSession(ctx, r.ID, r.Fields["expected_revision"])
		case "checklist.load", "checklist.add_manual", "checklist.set_state":
			fields := map[string]any{"job_id": r.ID}
			for key, value := range r.Fields {
				fields[key] = value
			}
			value, err = owner.Checklist(ctx, applicationchecklist.Request{Operation: strings.TrimPrefix(r.Operation, "checklist."), Fields: fields})
		default:
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "JOB_REQUEST_INVALID"}
		}
		out, problem := jobWriteResult(value, err)
		out, problem = mergeJobResult(prior, out, problem)
		if problem != nil {
			return out, problem
		}
		if r.Operation == "session.latest" {
			wrapped, e := jobReadResult(map[string]any{"contract_version": 1, "session": out.Result}, nil)
			return mergeJobResult(out, wrapped, e)
		}
		if r.Operation == "session.create" || r.Operation == "session.confirm" || r.Operation == "session.dismiss" || r.Operation == "session.cancel" {
			var session struct {
				JobID string `json:"job_id"`
			}
			if json.Unmarshal(out.Result, &session) != nil || !jobcard.ValidID(session.JobID) {
				return mergeJobResult(out, nil, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID"})
			}
			detail, err := w.Application(ctx, session.JobID)
			wrapped, e := jobReadResult(map[string]any{"session": out.Result, "application": detail}, err)
			return mergeJobResult(out, wrapped, e)
		}
		return out, nil
	}
}

func resumeManagementResult(rows []readonly.ManagedResumeEntry, err error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if err != nil {
		code, category := "RESUME_MANAGEMENT_READ_FAILED", "read"
		var problem *readonly.Error
		if errors.As(err, &problem) {
			switch problem.Code {
			case "APPLICATION_INPUT_LIMIT", "APPLICATION_INPUT_UNSUPPORTED":
				code, category = "RESUME_MANAGEMENT_INVALID", "input"
			case "APPLICATION_RESULT_LIMIT", "RESUME_MANAGEMENT_RESULT_LIMIT", "RESUME_MANAGEMENT_REPORT_LIMIT":
				code, category = "RESUME_MANAGEMENT_LIMIT", "limit"
			case "APPLICATION_READ_CANCELED":
				code, category = "RESUME_MANAGEMENT_CANCELED", "canceled"
			case "RESUME_MANAGEMENT_DATABASE_CHANGED", "RESUME_MANAGEMENT_CATALOG_CHANGED", "RESUME_MANAGEMENT_RUNS_CHANGED":
				code, category = "RESUME_MANAGEMENT_CHANGED", "drift"
			case "APPLICATION_DATABASE_UNAVAILABLE", "APPLICATION_SCHEMA_TOO_NEW",
				"APPLICATION_SCHEMA_PREPARATION_REQUIRED", "APPLICATION_VALUE_UNSUPPORTED",
				"APPLICATION_READ_FAILED", "RESUME_MANAGEMENT_CATALOG_FAILED",
				"RESUME_MANAGEMENT_DATABASE_VALUE":
				code, category = "RESUME_MANAGEMENT_UNAVAILABLE", "storage"
			case "APPLICATION_PATH_REJECTED", "APPLICATION_STORAGE_UNSUPPORTED", "RESUME_MANAGEMENT_RUNS_REJECTED", "RESUME_MANAGEMENT_REPORT_REJECTED":
				code, category = "RESUME_MANAGEMENT_UNSAFE", "unsafe"
			case "APPLICATION_CLEANUP_FAILED", "RESUME_MANAGEMENT_CLEANUP_FAILED":
				code, category = "RESUME_MANAGEMENT_CLEANUP_FAILED", "cleanup"
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code, category = "RESUME_MANAGEMENT_CANCELED", "canceled"
		}
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: code, Category: category}
	}
	if rows == nil {
		rows = []readonly.ManagedResumeEntry{}
	}
	raw, marshalErr := json.Marshal(struct {
		Resumes []readonly.ManagedResumeEntry `json:"resumes"`
	}{rows})
	if marshalErr != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID"}
	}
	return &httpapi.ApplicationOutcome{Result: raw}, nil
}

func resumeDigestResult(resumeID string, value *applicationwrite.Outcome, err *applicationwrite.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := &httpapi.ApplicationOutcome{}
	if value != nil {
		out.Result = value.Result
		out.CleanupFailed = value.CleanupFailed
		out.RecoveryRequired = value.RecoveryRequired
		out.Partial = value.IntakeReportPublished || value.RecoveryRequired
		out.CommitUnknown = value.IntakeState == "published_unconfirmed"
	}
	if err != nil {
		code := "RESUME_DIGEST_FAILED"
		if value != nil && value.IntakeState == "published_unconfirmed" {
			code = "RESUME_DIGEST_COMPLETED_UNCONFIRMED"
		} else if value != nil && value.RecoveryRequired {
			code = "RESUME_DIGEST_RECOVERY_REQUIRED"
		} else if value != nil && value.CleanupFailed {
			code = "RESUME_DIGEST_CLEANUP_FAILED"
		} else {
			switch err.Code {
			case "INTAKE_RESUME_ID_INVALID":
				code = "RESUME_DIGEST_REQUEST_INVALID"
			case "INTAKE_RESUME_NOT_FOUND":
				code = "RESUME_DIGEST_NOT_FOUND"
			case "INTAKE_RESUME_ARCHIVED":
				code = "RESUME_DIGEST_ARCHIVED"
			case "INTAKE_FORMAT_UNSUPPORTED":
				code = "RESUME_DIGEST_UNSUPPORTED"
			case "INTAKE_SOURCE_UNAVAILABLE", "INTAKE_SOURCE_PATH_INVALID":
				code = "RESUME_DIGEST_SOURCE_INVALID"
			case "INTAKE_SOURCE_CHANGED", "INTAKE_WITNESS_CHANGED", "INTAKE_HOME_CHANGED", "APPLICATION_LEDGER_HOME_CHANGED":
				code = "RESUME_DIGEST_INPUT_CHANGED"
			case "INTAKE_EXTRACTION_LIMIT", "INTAKE_REPORT_LIMIT", "INTAKE_IDENTITY_COLLISION":
				code = "RESUME_DIGEST_LIMIT"
			case "INTAKE_CANCELED", "APPLICATION_LEDGER_CANCELED":
				code = "RESUME_DIGEST_CANCELED"
			case "INTAKE_WRITER_UNAVAILABLE", "INTAKE_DATABASE_UNAVAILABLE", "SCHEMA_PREPARATION_REQUIRED", "APPLICATION_SCHEMA_PREPARATION_REQUIRED", "APPLICATION_SCHEMA_TOO_NEW", "DATA_HOME_OWNERSHIP_REQUIRED":
				code = "RESUME_DIGEST_UNAVAILABLE"
			default:
				if strings.HasPrefix(err.Code, "PATH_") || strings.HasPrefix(err.Code, "ROOT_") {
					code = "RESUME_DIGEST_UNSAFE"
				}
			}
		}
		out.Result = nil
		return out, &httpapi.ApplicationError{Code: code, Category: "resume_digest"}
	}
	var result struct {
		ResumeID        string `json:"resume_id"`
		SourceID        string `json:"source_id"`
		TechnologyCount int    `json:"technology_count"`
		BulletCount     int    `json:"bullet_count"`
	}
	if value == nil || !value.IntakeReportPublished || value.RecoveryRequired || value.IntakeState != "published" ||
		json.Unmarshal(value.Result, &result) != nil || result.ResumeID != resumeID || !nativeDigestSourceID(result.SourceID) ||
		result.TechnologyCount < 0 || result.TechnologyCount > 4096 || result.BulletCount < 0 || result.BulletCount > 4096 {
		out.Result = nil
		return out, &httpapi.ApplicationError{Code: "RESUME_DIGEST_FAILED", Category: "resume_digest"}
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		out.Result = nil
		return out, &httpapi.ApplicationError{Code: "RESUME_DIGEST_FAILED", Category: "resume_digest"}
	}
	out.Result = raw
	out.Partial = false
	return out, nil
}

func nativeDigestSourceID(value string) bool {
	if len(value) < 5 || len(value) > 512 || !strings.HasPrefix(value, "src_") || !utf8.ValidString(value) {
		return false
	}
	for _, point := range value[4:] {
		if point != '_' && (point < 'a' || point > 'z') && (point < '0' || point > '9') {
			return false
		}
	}
	return true
}

func resumeJobResult(operation string, value *applicationwrite.ResumeOutcome, err *applicationwrite.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	var base *applicationwrite.Outcome
	if value != nil {
		base = value.Outcome
	}
	out := feedWriteOutcome(base)
	if err == nil {
		return out, nil
	}
	if operation == "resume.submitted_file" || operation == "resume.attachment_file" {
		prefix := "APPLICATION_SUBMITTED_RESUME_"
		if operation == "resume.attachment_file" {
			prefix = "APPLICATION_RESUME_"
		}
		code := prefix + "READ_FAILED"
		switch err.SourceClass {
		case "ApplicationNotFound":
			code = prefix + "NOT_FOUND"
		case "ApplicationHistoryError":
			code = prefix + "INTEGRITY_FAILED"
		}
		if err.Code == "APPLICATION_LEDGER_CANCELED" || err.Code == "RESUME_LIBRARY_CANCELED" {
			code = "LOCAL_OPERATION_CANCELED"
		}
		return out, &httpapi.ApplicationError{Code: code}
	}
	code := "RESUME_LIBRARY_READ_FAILED"
	if operation == "resume.archive" {
		code = "RESUME_LIBRARY_ARCHIVE_FAILED"
	} else if operation == "resume.restore" {
		code = "RESUME_LIBRARY_RESTORE_FAILED"
	} else if operation == "resume.upload" {
		code = "RESUME_LIBRARY_UPLOAD_FAILED"
	} else if operation == "resume.attach" {
		code = "APPLICATION_RESUME_SAVE_FAILED"
	} else if operation == "resume.delete" {
		code = "RESUME_VERSION_DELETE_FAILED"
	}
	switch err.SourceClass {
	case "ApplicationNotFound":
		if operation == "resume.attach" {
			code = "APPLICATION_JOB_NOT_FOUND"
		}
	case "ResumeNotFound":
		code = "RESUME_LIBRARY_NOT_FOUND"
	case "ResumeVersionInUse":
		code = "RESUME_VERSION_IN_USE"
	case "ResumeVersionFileMissing":
		code = "RESUME_VERSION_FILE_MISSING"
	case "ResumeVersionFileInvalid":
		code = "RESUME_VERSION_FILE_INVALID"
	case "ApplicationHistoryError":
		if operation == "resume.attach" {
			code = "APPLICATION_RESUME_INVALID"
		} else if operation == "resume.file" {
			code = "RESUME_LIBRARY_READ_INVALID"
		} else if operation == "resume.restore" {
			code = "RESUME_LIBRARY_RESTORE_INVALID"
		} else if operation == "resume.upload" {
			code = "RESUME_LIBRARY_UPLOAD_INVALID"
		} else if operation == "resume.delete" {
			code = "RESUME_VERSION_DELETE_INVALID"
		} else {
			code = "RESUME_LIBRARY_ARCHIVE_INVALID"
		}
	}
	if operation == "resume.upload" || operation == "resume.attach" {
		switch err.Code {
		case "RESUME_LIBRARY_INPUT_LIMIT", "RESUME_LIBRARY_UNICODE_UNSUPPORTED", "RESUME_LIBRARY_INPUT_UNSUPPORTED":
			if operation == "resume.attach" {
				code = "APPLICATION_RESUME_INVALID"
			} else {
				code = "RESUME_LIBRARY_UPLOAD_INVALID"
			}
		case "RESUME_LIBRARY_IDENTITY_COLLISION":
			code = "RESUME_VERSION_IDENTITY_COLLISION"
		}
	}
	if err.Code == "APPLICATION_LEDGER_CANCELED" || err.Code == "RESUME_LIBRARY_CANCELED" {
		code = "LOCAL_OPERATION_CANCELED"
	}
	return out, &httpapi.ApplicationError{Code: code}
}

func applicationResumeError(problem *httpapi.ApplicationError) *httpapi.ApplicationError {
	if problem == nil {
		return nil
	}
	code := "APPLICATION_RESUME_SAVE_FAILED"
	switch problem.Code {
	case "JOB_CARD_NOT_FOUND":
		code = "APPLICATION_JOB_NOT_FOUND"
	case "JOB_CARD_INVALID", "JOB_REQUEST_INVALID":
		code = "APPLICATION_RESUME_INVALID"
	case "LOCAL_OPERATION_CANCELED":
		code = problem.Code
	}
	return &httpapi.ApplicationError{Code: code, Category: problem.Category}
}

func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}
func optionalTimestamp(value any) any {
	if value == "" {
		return nil
	}
	return value
}

func jobReadResult(value any, err error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if err != nil {
		code := "JOB_LIBRARY_UNAVAILABLE"
		var p *readonly.Error
		if errors.As(err, &p) {
			switch p.Code {
			case "JOB_CARD_NOT_FOUND", "JOB_CARD_INVALID", "JOB_LIBRARY_CHANGED", "JOB_QUERY_INVALID", "JOB_QUERY_TOO_BROAD":
				code = p.Code
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = "LOCAL_OPERATION_CANCELED"
		}
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: code}
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID"}
	}
	return &httpapi.ApplicationOutcome{Result: raw}, nil
}

func jobWriteResult(value *applicationwrite.Outcome, err *applicationwrite.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := feedWriteOutcome(value)
	if err == nil {
		return out, nil
	}
	code := "JOB_OPERATION_FAILED"
	switch err.SourceClass {
	case "ApplicationNotFound", "ApplicationSessionNotFound":
		code = "JOB_CARD_NOT_FOUND"
	case "ApplicationSessionRevisionConflict", "ApplicationInstructionRevisionConflict", "JobAssessmentRevisionConflict", "ApplicationRevisionConflict":
		code = "JOB_REVISION_CONFLICT"
	case "ResumeVersionArchived":
		code = "JOB_REVISION_CONFLICT"
	case "ApplicationHistoryError", "ApplicationSessionError", "ApplicationInstructionChecklistError":
		code = "JOB_REQUEST_INVALID"
	}
	switch err.Code {
	case "JOB_CARD_NOT_FOUND", "SAVED_ASSESSMENT_JOB_NOT_FOUND":
		code = "JOB_CARD_NOT_FOUND"
	case "SAVED_ASSESSMENT_JOB_INVALID", "SAVED_ASSESSMENT_JOB_ID_INVALID":
		code = "JOB_CARD_INVALID"
	case "SAVED_ASSESSMENT_REVISION_CONFLICT":
		code = "JOB_REVISION_CONFLICT"
	case "SAVED_ASSESSMENT_REQUEST_INVALID", "SAVED_ASSESSMENT_REQUEST_LIMIT", "SAVED_ASSESSMENT_RESOLUTION_INVALID", "SAVED_ASSESSMENT_FINDING_NOT_FOUND":
		code = "JOB_REQUEST_INVALID"
	case "APPLICATION_LEDGER_CANCELED", "SAVED_ASSESSMENT_CANCELED":
		code = "LOCAL_OPERATION_CANCELED"
	}
	return out, &httpapi.ApplicationError{Code: code}
}

func jobDetailsWriteResult(value *applicationwrite.Outcome, err *applicationwrite.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := feedWriteOutcome(value)
	if err == nil {
		return out, nil
	}
	code := "JOB_DETAILS_UNAVAILABLE"
	switch err.Code {
	case "JOB_DETAILS_REQUEST_INVALID", "JOB_DETAILS_INVALID":
		code = "JOB_DETAILS_REQUEST_INVALID"
	case "JOB_CARD_NOT_FOUND":
		code = "JOB_CARD_NOT_FOUND"
	case "JOB_DETAILS_CARD_INVALID", "JOB_DETAILS_SOURCE_INVALID":
		code = "JOB_CARD_INVALID"
	case "JOB_DETAILS_REVISION_CONFLICT", "JOB_DETAILS_CARD_CHANGED", "JOB_DETAILS_SOURCE_CHANGED":
		code = "JOB_DETAILS_REVISION_CONFLICT"
	case "APPLICATION_LEDGER_CANCELED":
		code = "LOCAL_OPERATION_CANCELED"
	}
	if out != nil && (out.CommitUnknown || out.Committed) {
		code = "JOB_DETAILS_SAVE_UNVERIFIED"
	}
	return out, &httpapi.ApplicationError{Code: code}
}

// A later read/write failure cannot erase an earlier registration or status
// commit. Unknown outcomes and recovery requirements remain visible to the UI.
func mergeJobResult(prior, next *httpapi.ApplicationOutcome, err *httpapi.ApplicationError) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if prior == nil {
		return next, err
	}
	prior.Result = nil
	if next != nil {
		prior.Result = next.Result
		prior.Committed = prior.Committed || next.Committed
		prior.CommitUnknown = prior.CommitUnknown || next.CommitUnknown
		prior.CleanupFailed = prior.CleanupFailed || next.CleanupFailed
		prior.RecoveryRequired = prior.RecoveryRequired || next.RecoveryRequired
		prior.Partial = prior.Partial || next.Partial
	}
	if err != nil {
		prior.Result = nil
		prior.Partial = prior.Partial || prior.Committed
	}
	return prior, err
}
