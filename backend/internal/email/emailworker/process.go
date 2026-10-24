package emailworker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"careerhub.local/backend/internal/email/emailalerts"
	"careerhub.local/backend/internal/email/emailheaders"
	"careerhub.local/backend/internal/email/emailmime"
	"careerhub.local/backend/internal/email/emailreceipts"
	"careerhub.local/backend/internal/email/emailrouting"
)

func newLeaseOwner() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return hex.EncodeToString(raw[:]), nil
}

func contentHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func terminal(status string) bool {
	switch status {
	case "imported", "no_candidates", "duplicate", "too_large", "failed_final":
		return true
	}
	return false
}

func metadataInput(epoch string, uid uint32, header emailheaders.Metadata, now func() time.Time) emailreceipts.RecordInput {
	return emailreceipts.RecordInput{
		UIDValidity: epoch, UID: uid, SenderAddress: header.SenderAddress,
		MessageIDHash: header.MessageIDHash, RecordedAt: utcTimestamp(now),
		MessageSentDate:          &header.MessageSentDate,
		SubjectJobSignal:         &header.SubjectJobSignal,
		SubjectServiceSignal:     &header.SubjectServiceSignal,
		SubjectApplicationSignal: &header.SubjectApplicationSignal,
	}
}

func record(ctx context.Context, state State, input emailreceipts.RecordInput) error {
	_, err := state.Record(ctx, input)
	return err
}

func processUID(
	ctx context.Context, state State, mailbox Mailbox, importer Importer,
	rules map[string]emailrouting.Rule, epoch string, uid uint32, now func() time.Time,
	result *Result, issues, retryPending *bool,
) error {
	existing, err := state.Receipt(ctx, epoch, uid)
	if err != nil {
		return err
	}
	if existing != nil && terminal(existing.Status) {
		return record(ctx, state, emailreceipts.RecordInput{
			UIDValidity: epoch, UID: uid, SenderAddress: existing.SenderAddress,
			MessageIDHash: existing.MessageIDHash, ContentSHA256: existing.ContentSHA256,
			Status: existing.Status, CandidateCount: existing.CandidateCount,
			DiscoveryCount: existing.DiscoveryCount, ErrorCode: existing.ErrorCode,
			AdvanceCursor: true, RecordedAt: utcTimestamp(now),
		})
	}
	headers, err := mailbox.FetchHeaders(ctx, uid)
	if err != nil {
		return err
	}
	if headers.UID != uid || headers.SizeBytes < 0 {
		return failure("EMAIL_SYNC_PROTOCOL_FAILED")
	}
	result.Counts.HeadersFetched++
	header, err := emailheaders.Parse(headers.RawHeaders)
	if err != nil {
		return err
	}
	base := metadataInput(epoch, uid, header, now)
	duplicate, err := state.IdentityProcessed(ctx, "message_id_hash", header.MessageIDHash, epoch, uid)
	if err != nil {
		return err
	}
	if duplicate {
		base.Status, base.AdvanceCursor = "duplicate", true
		if err := record(ctx, state, base); err != nil {
			return err
		}
		result.Counts.MessagesDuplicate++
		return nil
	}
	rule, approved := rules[header.SenderAddress]
	if !approved {
		base.Status, base.ErrorCode, base.AdvanceCursor = "unknown_sender", "EMAIL_SYNC_SENDER_NOT_ALLOWED", true
		if err := record(ctx, state, base); err != nil {
			return err
		}
		result.Counts.MessagesUnknownSender++
		return nil
	}
	route := emailrouting.RoutedPurpose(rule.Purpose, rule.Policy, header.SubjectNormalized)
	if route == "" {
		filtered := "filtered"
		base.Status, base.ErrorCode, base.AdvanceCursor, base.SubjectRoute = "no_candidates", "EMAIL_SUBJECT_POLICY_FILTERED", true, &filtered
		if err := record(ctx, state, base); err != nil {
			return err
		}
		result.Counts.MessagesFilteredSubject++
		return nil
	}
	base.SubjectRoute = &route
	if headers.SizeBytes > emailmime.MaxEmailBytes {
		return tooLarge(ctx, state, base, result, issues)
	}
	message, err := mailbox.FetchMessage(ctx, uid)
	if err != nil {
		return err
	}
	result.Counts.BodiesFetched++
	if len(message) > emailmime.MaxEmailBytes {
		return tooLarge(ctx, state, base, result, issues)
	}
	base.ContentSHA256 = contentHash(message)
	duplicate, err = state.IdentityProcessed(ctx, "content_sha256", base.ContentSHA256, epoch, uid)
	if err != nil {
		return err
	}
	if duplicate {
		base.Status, base.AdvanceCursor = "duplicate", true
		if err := record(ctx, state, base); err != nil {
			return err
		}
		result.Counts.MessagesDuplicate++
		return nil
	}
	parsed, parseErr := emailalerts.Parse(message)
	imported := ImportResult{}
	if parseErr != nil {
		imported = ImportResult{Status: "failed", ErrorCode: safeCode(parseErr, "EMAIL_PARSE_FAILED")}
	} else {
		imported, err = importer.Import(ctx, parsed, route)
		if err != nil {
			return err
		}
		if !validImportResult(imported) ||
			imported.CandidateCount > len(parsed.Candidates) || imported.ApplicationOutcomeCount > len(parsed.ApplicationOutcomes) ||
			(route == emailrouting.SenderPurposeJobAlerts && imported.ApplicationOutcomeCount != 0) ||
			(route == emailrouting.SenderPurposeApplicationTracking && imported.CandidateCount != 0) {
			return failure("EMAIL_SYNC_PROTOCOL_FAILED")
		}
	}
	result.Counts.CandidatesFound += imported.CandidateCount
	result.Counts.DiscoveriesWritten += imported.DiscoveryCount
	result.Counts.ApplicationOutcomesFound += imported.ApplicationOutcomeCount
	result.Counts.ApplicationsUpdated += imported.ApplicationsUpdated
	result.Counts.DiscoveriesDismissed += imported.DiscoveriesDismissed
	result.Counts.ApplicationOutcomesUnlinked += imported.ApplicationOutcomesUnlinked
	result.Counts.ApplicationOutcomesNeedsReview += imported.ApplicationOutcomesNeedsReview
	if imported.Status == "imported" || imported.Status == "no_candidates" {
		base.Status, base.CandidateCount, base.DiscoveryCount, base.AdvanceCursor = imported.Status, imported.CandidateCount, imported.DiscoveryCount, true
		if err := record(ctx, state, base); err != nil {
			return err
		}
		if imported.Status == "imported" {
			result.Counts.MessagesImported++
		} else {
			result.Counts.MessagesWithoutCandidates++
		}
		return nil
	}
	code := strings.ToUpper(strings.TrimSpace(imported.ErrorCode))
	if !validCode.MatchString(code) {
		code = "EMAIL_IMPORT_FAILED"
	}
	attempt := 1
	if existing != nil {
		attempt = existing.AttemptCount + 1
	}
	final := attempt >= MaxFailureAttempts
	base.Status, base.CandidateCount, base.DiscoveryCount = "failed_retryable", imported.CandidateCount, imported.DiscoveryCount
	if final {
		base.Status, base.AdvanceCursor = "failed_final", true
	}
	base.ErrorCode = code
	if err := record(ctx, state, base); err != nil {
		return err
	}
	result.Counts.MessagesFailed++
	if final {
		result.Counts.MessagesFailedFinal++
	} else {
		result.Counts.MessagesRetryPending++
		*retryPending = true
	}
	*issues = true
	result.FailureCodeCounts[code]++
	return nil
}

func tooLarge(ctx context.Context, state State, input emailreceipts.RecordInput, result *Result, issues *bool) error {
	input.Status, input.ErrorCode, input.AdvanceCursor = "too_large", "EMAIL_TOO_LARGE", true
	if err := record(ctx, state, input); err != nil {
		return err
	}
	result.Counts.MessagesTooLarge++
	result.FailureCodeCounts["EMAIL_TOO_LARGE"]++
	*issues = true
	return nil
}
