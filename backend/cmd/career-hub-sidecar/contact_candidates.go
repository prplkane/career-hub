package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
)

func contactCandidateAction(ctx context.Context, owner *applicationwrite.Service, request httpapi.JobRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	var value *applicationwrite.Outcome
	var problem *applicationwrite.Error
	switch request.Operation {
	case "contact.candidate_preview":
		if request.ContactCandidate == nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "CONTACT_CANDIDATE_REQUEST_INVALID"}
		}
		value, problem = owner.PreviewContactCandidate(ctx, *request.ContactCandidate)
		return contactCandidateResult(value, problem, false)
	case "contact.candidate_confirm":
		if request.ContactConfirmation == nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "CONTACT_CANDIDATE_REQUEST_INVALID"}
		}
		value, problem = owner.ConfirmContactCandidate(ctx, *request.ContactConfirmation)
		return contactCandidateResult(value, problem, true)
	default:
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "CONTACT_CANDIDATE_REQUEST_INVALID"}
	}
}

func contactCandidateProjection(raw json.RawMessage, confirmation bool) (bool, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil {
		return false, false
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) || value["contract_version"] != json.Number("1") {
		return false, false
	}
	if !confirmation {
		for _, key := range []string{"source", "candidate", "defaults", "matches", "claimed_contact"} {
			if _, ok := value[key]; !ok {
				return false, false
			}
		}
		return false, true
	}
	outcome, ok := value["outcome"].(string)
	if !ok || value["contact"] == nil {
		return false, false
	}
	switch outcome {
	case "created":
		return true, true
	case "provenance_match", "linked", "unchanged":
		return false, true
	default:
		return false, false
	}
}

// contactCandidateResult preserves the owning transaction's publication facts
// while allowing only the frozen public contact-candidate error vocabulary.
func contactCandidateResult(value *applicationwrite.Outcome, problem *applicationwrite.Error, confirmation bool) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := feedWriteOutcome(value)
	if problem == nil {
		if out == nil {
			code := "CONTACT_CANDIDATE_PREVIEW_FAILED"
			if confirmation {
				code = "CONTACT_CANDIDATE_SAVE_FAILED"
			}
			return nil, &httpapi.ApplicationError{Code: code}
		}
		created, valid := contactCandidateProjection(valueResult(value), confirmation)
		if !valid || (!confirmation && (out.Committed || out.CommitUnknown || out.Partial)) ||
			(created && (!out.Committed || out.CommitUnknown)) {
			code := "CONTACT_CANDIDATE_PREVIEW_FAILED"
			if confirmation {
				code = "CONTACT_CANDIDATE_SAVE_FAILED"
			}
			return out, &httpapi.ApplicationError{Code: code}
		}
		out.Created = created
		return out, nil
	}
	code := "CONTACT_CANDIDATE_PREVIEW_FAILED"
	if confirmation {
		code = "CONTACT_CANDIDATE_SAVE_FAILED"
	}
	switch problem.Code {
	case "CONTACT_CANDIDATE_REQUEST_INVALID", "CONTACT_CANDIDATE_PROTOCOL_UNSUPPORTED",
		"CONTACT_CANDIDATE_SOURCE_NOT_FOUND", "CONTACT_CANDIDATE_SOURCE_INCOMPLETE",
		"CONTACT_CANDIDATE_SOURCE_INVALID", "CONTACT_CANDIDATE_STALE",
		"CONTACT_EXACT_MATCH_REQUIRES_LINK", "CONTACT_CONFLICT":
		code = problem.Code
	case "APPLICATION_LEDGER_CANCELED", "APPLICATION_CONTACT_CANCELED":
		code = "LOCAL_OPERATION_CANCELED"
	case "APPLICATION_LEDGER_COMMIT_UNCONFIRMED":
		if confirmation {
			code = "CONTACT_CANDIDATE_SAVE_UNCONFIRMED"
		}
	default:
		switch problem.SourceClass {
		case "ApplicationContactError":
			code = "CONTACT_REQUEST_INVALID"
		case "ApplicationContactNotFound":
			code = "CONTACT_NOT_FOUND"
		case "ApplicationContactRevisionConflict", "ApplicationContactArchived",
			"ApplicationContactExactMatchAmbiguous", "ApplicationContactProvenanceConflict":
			code = "CONTACT_CONFLICT"
		}
	}
	return out, &httpapi.ApplicationError{Code: code}
}

func valueResult(value *applicationwrite.Outcome) json.RawMessage {
	if value == nil {
		return nil
	}
	return value.Result
}
