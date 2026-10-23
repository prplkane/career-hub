package applicationwrite

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationcontacts"
)

// ContactFields is a bounded first-party command, not an unrestricted HTTP
// object. Adapters must authorize contact writes independently of capture.
// Absent keys use source defaults; explicit null remains an invalid value.
type ContactFields map[string]any

func (ContactFields) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted contact fields]")
}

func (s *Service) contact(ctx context.Context, op string, fields map[string]any, order []string) (*Outcome, *Error) {
	if s == nil || s.contactEntropy == nil {
		return emptyFailure(failed("configuration", "APPLICATION_CONTACT_UNAVAILABLE"))
	}
	return s.run(ctx, "", func(_ *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, p := applicationcontacts.ApplyInTransaction(ctx, tx, applicationcontacts.Request{Operation: op, Fields: fields, UpdateOrder: order}, s.now(), s.contactEntropy)
			if p != nil {
				return nil, &Error{Stage: p.Stage, Code: p.Code, SourceClass: p.SourceClass, SourceMessage: p.SourceMessage}
			}
			return result, nil
		}, nil
	})
}
func (s *Service) CreateContact(ctx context.Context, fields ContactFields) (*Outcome, *Error) {
	return s.contact(ctx, "create", fields, nil)
}
func (s *Service) FindContacts(ctx context.Context, fields ContactFields) (*Outcome, *Error) {
	return s.contact(ctx, "find", fields, nil)
}
func (s *Service) GetContact(ctx context.Context, id any) (*Outcome, *Error) {
	return s.contact(ctx, "get", map[string]any{"contact_id": id}, nil)
}
func (s *Service) ListContacts(ctx context.Context, includeArchived any) (*Outcome, *Error) {
	return s.contact(ctx, "list", map[string]any{"include_archived": includeArchived}, nil)
}
func (s *Service) FindContactByJDProvenance(ctx context.Context, reference, hash any) (*Outcome, *Error) {
	return s.contact(ctx, "find_provenance", map[string]any{"source_reference": reference, "source_content_sha256": hash}, nil)
}
func (s *Service) UpdateContact(ctx context.Context, id, revision any, updates []applicationcontacts.FactChange, provenance any) (*Outcome, *Error) {
	fields := map[string]any{}
	order := []string{}
	for _, f := range updates {
		if _, exists := fields[f.Field]; exists {
			return emptyFailure(failed("request", "APPLICATION_CONTACT_UPDATE_ORDER_INVALID"))
		}
		fields[f.Field] = f.Value
		order = append(order, f.Field)
	}
	return s.contact(ctx, "update", map[string]any{"contact_id": id, "expected_revision": revision, "updates": fields, "provenance": provenance}, order)
}
func (s *Service) LinkContact(ctx context.Context, fields ContactFields) (*Outcome, *Error) {
	return s.contact(ctx, "link", fields, nil)
}
func (s *Service) UnlinkContact(ctx context.Context, fields ContactFields) (*Outcome, *Error) {
	return s.contact(ctx, "unlink", fields, nil)
}
func (s *Service) ArchiveContact(ctx context.Context, id, revision any) (*Outcome, *Error) {
	return s.contact(ctx, "archive", map[string]any{"contact_id": id, "expected_revision": revision}, nil)
}
func (s *Service) RestoreContact(ctx context.Context, id, revision any) (*Outcome, *Error) {
	return s.contact(ctx, "restore", map[string]any{"contact_id": id, "expected_revision": revision}, nil)
}
