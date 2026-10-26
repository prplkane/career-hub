// Package emailworker composes one bounded, synthetic-testable Gmail sync page.
// It does not open credentials, Data Home or a product route. Host-owned state
// and importer implementations are required before live activation.
package emailworker

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"time"

	"careerhub.local/backend/internal/email/emailalerts"
	"careerhub.local/backend/internal/email/emailreceipts"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/email/mailtransport"
)

const (
	ContractVersion    = 2
	MaxMessagesPerSync = 1000
	MaxLookbackDays    = 90
	MaxSyncDuration    = 120 * time.Second
	MaxFailureAttempts = 3
)

var validEpoch = regexp.MustCompile(`^[1-9][0-9]*$`)
var validCode = regexp.MustCompile(`^EMAIL_[A-Z0-9_]{1,80}$`)

// ErrCommitUnknown means the state/importer owner cannot prove the complete
// effect, including commit or required publication. The worker must stop
// without replaying or marking the lease failed.
var ErrCommitUnknown = errors.New("EMAIL_SYNC_COMMIT_UNKNOWN")

type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

func failure(code string) error { return &Error{Code: code} }

// State methods must commit synchronously before returning success and must
// preserve uncertain effects as ErrCommitUnknown. The host owns Data Home
// admission, SQLite path pins, recovery and serialization across all writers.
type State interface {
	Acquire(context.Context, string, string, bool) (string, error)
	SetUIDValidity(context.Context, string) (bool, error)
	Position(context.Context) (string, uint32, error)
	AllowedRules(context.Context) (map[string]emailrouting.Rule, error)
	PendingRetryUIDs(context.Context, string, map[string]bool) ([]uint32, error)
	Receipt(context.Context, string, uint32) (*emailreceipts.Receipt, error)
	IdentityProcessed(context.Context, string, string, string, uint32) (bool, error)
	Record(context.Context, emailreceipts.RecordInput) (int, error)
	Complete(context.Context, string, string, bool, bool) error
	Fail(context.Context, string, string, string) error
}

type Mailbox interface {
	UIDValidity() string
	SearchUIDs(context.Context, uint32, time.Time) ([]uint32, error)
	FetchHeaders(context.Context, uint32) (mailtransport.Headers, error)
	FetchMessage(context.Context, uint32) ([]byte, error)
	Close() error
}

type OpenMailbox func(context.Context) (Mailbox, error)

// Importer writes parsed candidates/outcomes idempotently through the host
// owner. It must return accurate committed effects and ErrCommitUnknown for an
// uncertain publication; an ordinary failure is returned as Status="failed".
type Importer interface {
	Import(context.Context, emailalerts.Result, string) (ImportResult, error)
}

type ImportResult struct {
	Status                                                             string
	CandidateCount, DiscoveryCount                                     int
	ApplicationOutcomeCount, ApplicationsUpdated, DiscoveriesDismissed int
	ApplicationOutcomesUnlinked, ApplicationOutcomesNeedsReview        int
	ErrorCode                                                          string
}

type Counts struct {
	UIDsFound                      int `json:"uids_found"`
	HeadersFetched                 int `json:"headers_fetched"`
	BodiesFetched                  int `json:"bodies_fetched"`
	MessagesImported               int `json:"messages_imported"`
	MessagesWithoutCandidates      int `json:"messages_without_candidates"`
	MessagesFilteredSubject        int `json:"messages_filtered_subject"`
	MessagesDuplicate              int `json:"messages_duplicate"`
	MessagesUnknownSender          int `json:"messages_unknown_sender"`
	MessagesTooLarge               int `json:"messages_too_large"`
	MessagesFailed                 int `json:"messages_failed"`
	MessagesRetryPending           int `json:"messages_retry_pending"`
	MessagesFailedFinal            int `json:"messages_failed_final"`
	CandidatesFound                int `json:"candidates_found"`
	DiscoveriesWritten             int `json:"discoveries_written"`
	ApplicationOutcomesFound       int `json:"application_outcomes_found"`
	ApplicationsUpdated            int `json:"applications_updated"`
	DiscoveriesDismissed           int `json:"discoveries_dismissed"`
	ApplicationOutcomesUnlinked    int `json:"application_outcomes_unlinked"`
	ApplicationOutcomesNeedsReview int `json:"application_outcomes_needs_review"`
}

type Result struct {
	ContractVersion   int            `json:"contract_version"`
	Status            string         `json:"status"`
	Counts            Counts         `json:"counts"`
	ErrorCode         string         `json:"error_code"`
	FailureCodeCounts map[string]int `json:"failure_code_counts"`
	HasMore           bool           `json:"has_more"`
	UIDValidityReset  bool           `json:"uidvalidity_reset"`
	CompletedAt       string         `json:"completed_at"`
	DeferredUntil     string         `json:"deferred_until"`
}

type Config struct {
	Trigger                   string // manual or scheduled
	ScheduleEnabled           bool
	LookbackDays, MaxMessages int
	Now                       func() time.Time
	Elapsed                   func() time.Duration
}

func safeCode(err error, fallback string) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrCommitUnknown) {
		return ErrCommitUnknown.Error()
	}
	var worker *Error
	if errors.As(err, &worker) && validCode.MatchString(worker.Code) {
		return worker.Code
	}
	var transport *mailtransport.Error
	if errors.As(err, &transport) && validCode.MatchString(transport.Code) {
		return transport.Code
	}
	if validCode.MatchString(err.Error()) {
		return err.Error()
	}
	return fallback
}

func utcTimestamp(now func() time.Time) string {
	return now().UTC().Format("2006-01-02T15:04:05Z")
}

func mergeUIDs(retry, found []uint32) ([]uint32, error) {
	if len(retry)+len(found) > 2*mailtransport.MaxSearchUIDs {
		return nil, failure("EMAIL_SYNC_RESPONSE_TOO_LARGE")
	}
	seen := make(map[uint32]bool, len(retry)+len(found))
	for _, uid := range retry {
		if uid == 0 {
			return nil, failure("EMAIL_SYNC_PROTOCOL_FAILED")
		}
		seen[uid] = true
	}
	for _, uid := range found {
		if uid == 0 {
			return nil, failure("EMAIL_SYNC_PROTOCOL_FAILED")
		}
		seen[uid] = true
	}
	all := make([]uint32, 0, len(seen))
	for uid := range seen {
		all = append(all, uid)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return all, nil
}

func validImportResult(result ImportResult) bool {
	if result.Status != "imported" && result.Status != "no_candidates" && result.Status != "partial" && result.Status != "failed" {
		return false
	}
	for _, count := range []int{result.CandidateCount, result.DiscoveryCount, result.ApplicationOutcomeCount,
		result.ApplicationsUpdated, result.DiscoveriesDismissed, result.ApplicationOutcomesUnlinked,
		result.ApplicationOutcomesNeedsReview} {
		if count < 0 {
			return false
		}
	}
	if result.DiscoveryCount > result.CandidateCount {
		return false
	}
	if result.Status == "no_candidates" && (result.CandidateCount != 0 || result.ApplicationOutcomeCount != 0) {
		return false
	}
	if result.Status == "imported" && result.CandidateCount == 0 && result.ApplicationOutcomeCount == 0 {
		return false
	}
	return true
}

// Run claims one lease, fetches one bounded UID page, and delegates each
// committed import to its explicit owner. No installed route calls it yet.
func Run(ctx context.Context, state State, open OpenMailbox, importer Importer, cfg Config) (Result, error) {
	result := Result{ContractVersion: ContractVersion, FailureCodeCounts: map[string]int{}}
	if ctx == nil || state == nil || open == nil || importer == nil || cfg.LookbackDays < 1 ||
		cfg.LookbackDays > MaxLookbackDays || cfg.MaxMessages < 1 || cfg.MaxMessages > MaxMessagesPerSync ||
		(cfg.Trigger != "manual" && cfg.Trigger != "scheduled") {
		return Result{}, failure("EMAIL_CONNECTION_INVALID")
	}
	if cfg.Trigger == "scheduled" && !cfg.ScheduleEnabled {
		result.Status = "disabled"
		return result, nil
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	started := now().UTC()
	startedTick := time.Now()
	elapsed := cfg.Elapsed
	if elapsed == nil {
		elapsed = func() time.Duration { return time.Since(startedTick) }
	}
	owner, err := newLeaseOwner()
	if err != nil {
		return Result{}, failure("EMAIL_SYNC_LOCAL_WRITE_FAILED")
	}
	deferred, err := state.Acquire(ctx, utcTimestamp(func() time.Time { return started }), owner, cfg.Trigger == "scheduled")
	if err != nil {
		return Result{}, failure(safeCode(err, "EMAIL_SYNC_LOCAL_WRITE_FAILED"))
	}
	if deferred != "" {
		result.Status, result.DeferredUntil = "deferred", deferred
		return result, nil
	}
	abort := func(problem error) (Result, error) {
		code := safeCode(problem, "EMAIL_SYNC_LOCAL_WRITE_FAILED")
		if code == ErrCommitUnknown.Error() {
			return Result{}, failure(code)
		}
		if e := state.Fail(context.WithoutCancel(ctx), owner, code, utcTimestamp(now)); e != nil {
			return Result{}, failure(safeCode(e, "EMAIL_SYNC_LOCAL_WRITE_FAILED"))
		}
		return Result{}, failure(code)
	}
	mailbox, err := open(ctx)
	if err != nil {
		return abort(err)
	}
	if mailbox == nil {
		return abort(failure("EMAIL_SYNC_PROTOCOL_FAILED"))
	}
	defer mailbox.Close()
	epoch := mailbox.UIDValidity()
	if !validEpoch.MatchString(epoch) {
		return abort(failure("EMAIL_SYNC_UIDVALIDITY_INVALID"))
	}
	result.UIDValidityReset, err = state.SetUIDValidity(ctx, epoch)
	if err != nil {
		return abort(err)
	}
	storedEpoch, lastUID, err := state.Position(ctx)
	if err != nil {
		return abort(err)
	}
	if storedEpoch != epoch {
		return abort(failure("EMAIL_SYNC_PROTOCOL_FAILED"))
	}
	rules, err := state.AllowedRules(ctx)
	if err != nil {
		return abort(err)
	}
	allowed := make(map[string]bool, len(rules))
	for address := range rules {
		allowed[address] = true
	}
	retryUIDs, err := state.PendingRetryUIDs(ctx, epoch, allowed)
	if err != nil {
		return abort(err)
	}
	var foundUIDs []uint32
	if lastUID < ^uint32(0) {
		lookback := started.AddDate(0, 0, -cfg.LookbackDays)
		since := time.Date(lookback.Year(), lookback.Month(), lookback.Day(), 0, 0, 0, 0, time.UTC)
		foundUIDs, err = mailbox.SearchUIDs(ctx, lastUID+1, since)
		if err != nil {
			return abort(err)
		}
	}
	uids, err := mergeUIDs(retryUIDs, foundUIDs)
	if err != nil {
		return abort(err)
	}
	result.Counts.UIDsFound = len(uids)
	result.HasMore = len(uids) > cfg.MaxMessages
	issues, retryPending := false, false
	for i, uid := range uids {
		if i >= cfg.MaxMessages {
			break
		}
		if elapsed() >= MaxSyncDuration {
			result.HasMore = true
			break
		}
		if err := processUID(ctx, state, mailbox, importer, rules, epoch, uid, now, &result, &issues, &retryPending); err != nil {
			return abort(err)
		}
	}
	completed := utcTimestamp(now)
	if err := state.Complete(ctx, owner, completed, issues, retryPending); err != nil {
		return abort(err)
	}
	result.CompletedAt = completed
	result.Status = "succeeded"
	if issues {
		result.Status = "partial"
	}
	if retryPending {
		result.ErrorCode = "EMAIL_SYNC_MESSAGE_RETRY"
	} else if issues {
		result.ErrorCode = "EMAIL_SYNC_MESSAGE_SKIPPED"
	}
	return result, nil
}
