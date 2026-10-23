package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/applications/applicationcontacts"
	"careerhub.local/backend/internal/applications/contactcandidate"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/jobs/jobcard"
)

func candidateProblem(p *contactcandidate.Problem) *Error {
	if p == nil {
		return nil
	}
	return &Error{
		Stage: "contact_candidate", Code: p.Code,
		SourceClass: "ContactCandidateRequestError", SourceMessage: p.Message,
	}
}

func candidateSourceProblem(code, message string) *Error {
	return candidateProblem(&contactcandidate.Problem{Code: code, Status: 409, Message: message})
}

func candidateSourceNotFound(message string) *Error {
	return candidateProblem(&contactcandidate.Problem{Code: "CONTACT_CANDIDATE_SOURCE_NOT_FOUND", Status: 404, Message: message})
}

func decodedCandidateSource(raw []byte) (string, *Error) {
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(raw) || len(raw) > applicationanalysis.MaxSourceBytes {
		return "", candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "The current job source is unavailable")
	}
	return strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n"), nil
}

func (s *Service) candidateJobMaterial(reads *sourceReads, request contactcandidate.SourceRequest) (contactcandidate.Material, map[string]any, *Error) {
	path := ""
	if jobcard.ValidID(request.SourceID) {
		path = "jobs/normalized/" + request.SourceID + ".yaml"
		loaded, p := jobcard.Read(reads.ctx, reads, path)
		if p == nil {
			id := cardField(loaded.Card, "id").Text
			if loaded.Report == nil || !loaded.Report.SchemaValid() || !loaded.RawPresent || id != request.SourceID {
				return contactcandidate.Material{}, nil, candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "Invalid job cards cannot provide contact evidence")
			}
			input := map[string]any{
				"id": id, "raw_jd_path": cardField(loaded.Card, "raw_jd_path").Text,
				"company":    cardField(loaded.Card, "company").Text,
				"title":      cardField(loaded.Card, "title").Text,
				"source_url": cardField(loaded.Card, "source_url").Text,
			}
			for _, pair := range loaded.Card.Entries {
				if pair.Key.Kind == "string" && pair.Key.Text == "content_fingerprint" {
					if pair.Value.Kind == "string" {
						input["content_fingerprint"] = pair.Value.Text
					} else {
						input["content_fingerprint"] = nil
					}
				}
			}
			return contactcandidate.Material{
				State: "complete",
				Source: contactcandidate.Source{
					Kind: "job", ID: request.SourceID,
					Title: cardField(loaded.Card, "title").Text, Company: cardField(loaded.Card, "company").Text,
				},
				SourceText: strings.TrimPrefix(loaded.RawText, "\ufeff"),
			}, input, nil
		}
		if p.Code != "JOB_CARD_NOT_FOUND" {
			return contactcandidate.Material{}, nil, candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "Invalid job cards cannot provide contact evidence")
		}
	}

	// Legacy cards are normally canonical <id>.yaml files. A closed directory
	// scan is retained only as a compatibility fallback for older filenames;
	// duplicate or invalid identities fail closed rather than selecting one.
	cards, e := s.loadNormalized(reads, false)
	if e != nil {
		return contactcandidate.Material{}, nil, candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "The current job source is unavailable")
	}
	if cards.invalidIDs[request.SourceID] {
		return contactcandidate.Material{}, nil, candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "Invalid job cards cannot provide contact evidence")
	}
	var selected map[string]any
	for _, input := range cards.inputs {
		if input["id"] == request.SourceID {
			if selected != nil {
				return contactcandidate.Material{}, nil, candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "Invalid job cards cannot provide contact evidence")
			}
			selected = input
		}
	}
	if selected == nil {
		return contactcandidate.Material{}, nil, candidateSourceNotFound("Job was not found")
	}
	rawPath, ok := selected["raw_jd_path"].(string)
	if !ok || rawPath == "" {
		return contactcandidate.Material{}, nil, candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "The current job source is unavailable")
	}
	raw, present, pe := reads.ReadFile(rawPath, applicationanalysis.MaxSourceBytes)
	if pe != nil || !present {
		return contactcandidate.Material{}, nil, candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "The current job source is unavailable")
	}
	text, p := decodedCandidateSource(raw)
	if p != nil {
		return contactcandidate.Material{}, nil, p
	}
	title, _ := selected["title"].(string)
	company, _ := selected["company"].(string)
	return contactcandidate.Material{
		State: "complete", Source: contactcandidate.Source{
			Kind: "job", ID: request.SourceID, Title: title, Company: company,
		}, SourceText: text,
	}, selected, nil
}

func candidateDiscoveryMaterial(ctx context.Context, tx *sql.Tx, reads *sourceReads, request contactcandidate.SourceRequest) (contactcandidate.Material, *Error) {
	capture, p := discovery.ReadCaptureInTransaction(ctx, tx, reads, request.SourceID)
	if p != nil {
		if p.Code == "NOT_FOUND" {
			return contactcandidate.Material{}, candidateSourceNotFound("Discovery was not found")
		}
		return contactcandidate.Material{}, candidateSourceProblem("CONTACT_CANDIDATE_SOURCE_INVALID", "The current discovery source is unavailable")
	}
	state := "incomplete"
	if capture.ContentScope == "page_visible" || capture.ContentScope == "confirmed_full" {
		state = "complete"
	}
	return contactcandidate.Material{
		State: state, Source: contactcandidate.Source{
			Kind: "discovery", ID: request.SourceID, Title: capture.Title, Company: capture.Company,
		}, SourceText: capture.SourceText,
	}, nil
}

func (s *Service) resolveContactCandidate(ctx context.Context, tx *sql.Tx, reads *sourceReads, request contactcandidate.SourceRequest) (contactcandidate.Resolved, map[string]any, *Error) {
	var material contactcandidate.Material
	var jobInput map[string]any
	var p *Error
	if request.SourceKind == "discovery" {
		material, p = candidateDiscoveryMaterial(ctx, tx, reads, request)
	} else {
		material, jobInput, p = s.candidateJobMaterial(reads, request)
	}
	if p != nil {
		return contactcandidate.Resolved{}, nil, p
	}
	resolved, candidateErr := contactcandidate.Resolve(material, request)
	if candidateErr != nil {
		return contactcandidate.Resolved{}, nil, candidateProblem(candidateErr)
	}
	return resolved, jobInput, nil
}

func candidateContactRead(ctx context.Context, tx *sql.Tx, now string, entropy applicationcontacts.Entropy, operation string, fields map[string]any) (any, *Error) {
	result, p := applicationcontacts.ApplyInTransaction(ctx, tx, applicationcontacts.Request{Operation: operation, Fields: fields}, now, entropy)
	if p != nil {
		return nil, &Error{Stage: p.Stage, Code: p.Code, SourceClass: p.SourceClass, SourceMessage: p.SourceMessage}
	}
	if result == nil || result.Changed || len(result.Result) == 0 {
		return nil, failed("contact_candidate", "CONTACT_CANDIDATE_PREVIEW_FAILED")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(result.Result))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, failed("contact_candidate", "CONTACT_CANDIDATE_PREVIEW_FAILED")
	}
	return value, nil
}

func candidateChannels(resolved contactcandidate.Resolved) []any {
	channels := make([]any, 0, len(resolved.Defaults.Channels))
	for _, channel := range resolved.Defaults.Channels {
		channels = append(channels, map[string]any{"kind": channel.Kind, "value": channel.Value})
	}
	return channels
}

// PreviewContactCandidate reopens and re-extracts one current server-owned JD
// source, then reads conservative contact matches in the same owning operation.
// It never creates a contact, registers a job, changes status or commits SQL.
func (s *Service) PreviewContactCandidate(ctx context.Context, request contactcandidate.SourceRequest) (*Outcome, *Error) {
	if p := contactcandidate.ValidateSourceRequest(request); p != nil {
		return emptyFailure(candidateProblem(p))
	}
	if s == nil || s.contactEntropy == nil {
		return emptyFailure(failed("configuration", "APPLICATION_CONTACT_UNAVAILABLE"))
	}
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			resolved, _, p := s.resolveContactCandidate(ctx, tx, reads, request)
			if p != nil {
				return nil, p
			}
			matches, p := candidateContactRead(ctx, tx, s.now(), s.contactEntropy, "find", map[string]any{
				"name": resolved.Defaults.Name, "role": resolved.Defaults.Role,
				"company": resolved.Defaults.Company, "context": resolved.Defaults.Context,
				"channels": candidateChannels(resolved),
			})
			if p != nil {
				return nil, p
			}
			claimed, p := candidateContactRead(ctx, tx, s.now(), s.contactEntropy, "find_provenance", map[string]any{
				"source_reference": resolved.SourceReference, "source_content_sha256": resolved.ContentSHA256,
			})
			if p != nil {
				return nil, p
			}
			body, marshalErr := json.Marshal(contactcandidate.ProjectPreview(resolved, matches, claimed))
			if marshalErr != nil {
				return nil, failed("contact_candidate", "CONTACT_CANDIDATE_PREVIEW_FAILED")
			}
			return &applications.MutationResult{Result: body, Changed: false}, nil
		}, nil
	})
}
