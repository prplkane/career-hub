package emailimport

import (
	"context"
	"encoding/json"
	"errors"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/email/emailalerts"
	"careerhub.local/backend/internal/email/emailcandidates"
	"careerhub.local/backend/internal/email/emailoutcomes"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/email/emailworker"
)

// Writer is the already-admitted native Data Home owner. Each method has its
// own commit boundary; a retry must be safe after earlier units were committed.
type Writer interface {
	CaptureDiscovery(context.Context, discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error)
	RecordEmailApplicationOutcome(context.Context, string, emailcandidates.ApplicationOutcome, string) (*applicationwrite.Outcome, *applicationwrite.Error)
}

// Importer connects only an already parsed and purpose-routed message to the
// native owners. It cannot open a mailbox, discover a Data Home or fetch a JD.
type Importer struct{ Writer Writer }

var _ Writer = (*applicationwrite.Service)(nil)
var _ emailworker.Importer = Importer{}

var errProtocol = errors.New("EMAIL_SYNC_PROTOCOL_FAILED")

// ownerResult never converts an uncertain mutation into a retryable per-unit
// failure. This includes confirmed SQL with failed publication/cleanup: the
// owner removes its response, so the complete effect cannot be counted.
func ownerResult(out *applicationwrite.Outcome, problem *applicationwrite.Error) (json.RawMessage, bool, error) {
	uncertain := out != nil && (out.CleanupFailed || out.RecoveryRequired ||
		(out.CommitAttempted && (!out.Committed || len(out.Result) == 0)) ||
		(out.Committed && !out.CommitAttempted))
	if uncertain || (problem != nil && out != nil && out.CommitAttempted) {
		return nil, false, emailworker.ErrCommitUnknown
	}
	if problem != nil || out == nil || len(out.Result) == 0 {
		return nil, false, nil
	}
	return out.Result, out.CommitAttempted, nil
}

// malformedResult stops replay if a committed owner returned an unusable
// response. A no-op with no commit can be handled as a retryable unit failure.
func malformedResult(committed bool) error {
	if committed {
		return emailworker.ErrCommitUnknown
	}
	return nil
}

func selectedUnits(route string, parsed emailalerts.Result) (int, int, error) {
	switch route {
	case emailrouting.SenderPurposeJobAlerts:
		return len(parsed.Candidates), 0, nil
	case emailrouting.SenderPurposeApplicationTracking:
		return 0, len(parsed.ApplicationOutcomes), nil
	case emailrouting.SenderPurposeBoth:
		return len(parsed.Candidates), len(parsed.ApplicationOutcomes), nil
	default:
		return 0, 0, errProtocol
	}
}

// Import preserves Python's outcome-first, then Discovery unit ordering. Each
// successful owner call is independently committed; a later known failure
// returns partial counts, while an uncertain effect aborts the worker page.
func (i Importer) Import(ctx context.Context, parsed emailalerts.Result, route string) (emailworker.ImportResult, error) {
	if ctx == nil || i.Writer == nil {
		return emailworker.ImportResult{}, errProtocol
	}
	candidateCount, outcomeCount, err := selectedUnits(route, parsed)
	if err != nil {
		return emailworker.ImportResult{}, err
	}
	if parsed.SchemaVersion != emailalerts.SchemaVersion || !fingerprintPattern.MatchString(parsed.MessageFingerprint) {
		return emailworker.ImportResult{Status: "failed", ErrorCode: "EMAIL_IMPORT_FILE_INVALID"}, nil
	}
	result := emailworker.ImportResult{CandidateCount: candidateCount, ApplicationOutcomeCount: outcomeCount}
	if candidateCount+outcomeCount == 0 {
		result.Status = "no_candidates"
		return result, nil
	}
	failedOutcomes, failedCandidates := 0, 0
	if outcomeCount != 0 {
		for _, outcome := range parsed.ApplicationOutcomes {
			out, problem := i.Writer.RecordEmailApplicationOutcome(ctx, parsed.MessageFingerprint, outcome, parsed.ReceivedAt)
			raw, committed, uncertainty := ownerResult(out, problem)
			if uncertainty != nil {
				return emailworker.ImportResult{}, uncertainty
			}
			if raw == nil {
				failedOutcomes++
				continue
			}
			var stored emailoutcomes.Result
			if json.Unmarshal(raw, &stored) != nil || stored.OutcomeID == "" || !validResolution(stored.Resolution) {
				if uncertainty := malformedResult(committed); uncertainty != nil {
					return emailworker.ImportResult{}, uncertainty
				}
				failedOutcomes++
				continue
			}
			if stored.ApplicationChanged {
				result.ApplicationsUpdated++
			}
			if stored.DiscoveryChanged {
				result.DiscoveriesDismissed++
			}
			switch stored.Resolution {
			case "unmatched", "discovery_dismissed":
				result.ApplicationOutcomesUnlinked++
			case "status_conflict", "ambiguous":
				result.ApplicationOutcomesNeedsReview++
			}
		}
	}
	if candidateCount != 0 {
		requests, e := ProjectCandidates(parsed, ChannelMailbox)
		if e != nil {
			failedCandidates = candidateCount
		} else {
			for _, request := range requests {
				out, problem := i.Writer.CaptureDiscovery(ctx, request)
				raw, committed, uncertainty := ownerResult(out, problem)
				if uncertainty != nil {
					return emailworker.ImportResult{}, uncertainty
				}
				if raw == nil {
					failedCandidates++
					continue
				}
				var captured struct {
					Outcome   string `json:"outcome"`
					Discovery struct {
						ID string `json:"discovery_id"`
					} `json:"discovery"`
				}
				if json.Unmarshal(raw, &captured) != nil ||
					captured.Discovery.ID == "" ||
					(captured.Outcome != "created" && captured.Outcome != "updated" && captured.Outcome != "duplicate") {
					if uncertainty := malformedResult(committed); uncertainty != nil {
						return emailworker.ImportResult{}, uncertainty
					}
					failedCandidates++
					continue
				}
				result.DiscoveryCount++
			}
		}
	}
	failures := failedOutcomes + failedCandidates
	if failures == 0 {
		result.Status = "imported"
		return result, nil
	}
	result.Status = "partial"
	if failures == candidateCount+outcomeCount {
		result.Status = "failed"
	}
	result.ErrorCode = "EMAIL_IMPORT_OUTCOME_FAILED"
	if failedCandidates != 0 {
		result.ErrorCode = "EMAIL_IMPORT_DISCOVERY_FAILED"
	}
	return result, nil
}

func validResolution(resolution string) bool {
	switch resolution {
	case "unmatched", "discovery_dismissed", "ambiguous", "status_conflict",
		"application_updated", "application_already_current":
		return true
	}
	return false
}
