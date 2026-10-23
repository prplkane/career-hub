package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationcontacts"
	"careerhub.local/backend/internal/applications/contactcandidate"
)

func candidateContactMap(value any) (map[string]any, *Error) {
	if value == nil {
		return nil, nil
	}
	record, ok := value.(map[string]any)
	if !ok {
		return nil, failed("contact_candidate", "CONTACT_CANDIDATE_SAVE_FAILED")
	}
	return record, nil
}

func candidateContactApply(ctx context.Context, tx *sql.Tx, now string, entropy applicationcontacts.Entropy, operation string, fields map[string]any) (map[string]any, bool, *Error) {
	result, p := applicationcontacts.ApplyInTransaction(ctx, tx, applicationcontacts.Request{Operation: operation, Fields: fields}, now, entropy)
	if p != nil {
		return nil, false, &Error{Stage: p.Stage, Code: p.Code, SourceClass: p.SourceClass, SourceMessage: p.SourceMessage}
	}
	if result == nil || len(result.Result) == 0 {
		return nil, false, failed("contact_candidate", "CONTACT_CANDIDATE_SAVE_FAILED")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(result.Result))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || value == nil {
		return nil, false, failed("contact_candidate", "CONTACT_CANDIDATE_SAVE_FAILED")
	}
	return value, result.Changed, nil
}

func candidateProvenance(resolved contactcandidate.Resolved, request contactcandidate.SourceRequest) map[string]any {
	proof := contactcandidate.CandidateProvenance(resolved, request)
	channels := make([]any, 0, len(proof.StatedFields.Channels))
	for _, channel := range proof.StatedFields.Channels {
		channels = append(channels, map[string]any{"kind": channel.Kind, "value": channel.Value})
	}
	return map[string]any{
		"origin": proof.Origin, "source_reference": proof.SourceReference,
		"discovery_id": proof.DiscoveryID, "job_id": proof.JobID,
		"source_content_sha256": proof.SourceContentSHA256,
		"source_text":           proof.SourceText, "line_start": proof.LineStart, "line_end": proof.LineEnd,
		"stated_fields": map[string]any{
			"name": proof.StatedFields.Name, "role": proof.StatedFields.Role,
			"company": proof.StatedFields.Company, "context": proof.StatedFields.Context,
			"channels": channels,
		},
	}
}

func candidateMutationFields(resolved contactcandidate.Resolved, request contactcandidate.ConfirmationRequest) (string, map[string]any) {
	jobIDs, discoveryIDs := []any{}, []any{}
	if request.SourceKind == "job" {
		jobIDs = append(jobIDs, request.SourceID)
	} else {
		discoveryIDs = append(discoveryIDs, request.SourceID)
	}
	fields := map[string]any{
		"provenance": candidateProvenance(resolved, request.SourceRequest),
		"job_ids":    jobIDs, "discovery_ids": discoveryIDs,
	}
	if request.Action == "create" {
		for _, key := range []string{"name", "role", "company", "context", "channels"} {
			fields[key] = request.Contact[key]
		}
		return "create", fields
	}
	fields["contact_id"] = request.ContactID
	fields["expected_revision"] = request.ExpectedRevision
	return "link", fields
}

func candidateProjectionResult(projection contactcandidate.Projection, changed bool) (*applications.MutationResult, *Error) {
	body, err := json.Marshal(projection.Body)
	if err != nil {
		return nil, failed("contact_candidate", "CONTACT_CANDIDATE_SAVE_FAILED")
	}
	return &applications.MutationResult{Result: body, Changed: changed}, nil
}

// ConfirmContactCandidate repeats current-source resolution and performs one
// explicit contact decision under the same operation gate and SQLite
// transaction. A saved job is registered as Captured before its contact link;
// neither step can commit without the other.
func (s *Service) ConfirmContactCandidate(ctx context.Context, request contactcandidate.ConfirmationRequest) (*Outcome, *Error) {
	if p := contactcandidate.ValidateConfirmationRequest(request); p != nil {
		return emptyFailure(candidateProblem(p))
	}
	if s == nil || s.contactEntropy == nil {
		return emptyFailure(failed("configuration", "APPLICATION_CONTACT_UNAVAILABLE"))
	}
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			resolved, jobInput, p := s.resolveContactCandidate(ctx, tx, reads, request.SourceRequest)
			if p != nil {
				return nil, p
			}
			now := s.now()
			claimedValue, p := candidateContactRead(ctx, tx, now, s.contactEntropy, "find_provenance", map[string]any{
				"source_reference": resolved.SourceReference, "source_content_sha256": resolved.ContentSHA256,
			})
			if p != nil {
				return nil, p
			}
			claimed, p := candidateContactMap(claimedValue)
			if p != nil {
				return nil, p
			}
			projection, handled, candidateErr := contactcandidate.ProjectClaimed(request, claimed)
			if candidateErr != nil {
				return nil, candidateProblem(candidateErr)
			}
			if handled {
				return candidateProjectionResult(projection, false)
			}

			changed := false
			if jobInput != nil {
				jobs, e := snapshots([]map[string]any{jobInput}, reads)
				if e != nil {
					return nil, e
				}
				registered, registerErr := applications.RegisterInTransaction(ctx, tx, jobs, now)
				if registerErr != nil {
					return nil, applicationProblem(registerErr, request.SourceID)
				}
				changed = registered.Changed
			}

			operation, fields := candidateMutationFields(resolved, request)
			result, contactChanged, p := candidateContactApply(ctx, tx, now, s.contactEntropy, operation, fields)
			if p != nil {
				return nil, p
			}
			projection, candidateErr = contactcandidate.ProjectMutation(request.Action, result)
			if candidateErr != nil {
				return nil, candidateProblem(candidateErr)
			}
			return candidateProjectionResult(projection, changed || contactChanged)
		}, nil
	})
}
