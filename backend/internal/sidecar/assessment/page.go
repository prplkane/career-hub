package assessment

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"time"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/previewcache"
	"careerhub.local/backend/internal/storage/operationgate"
)

// The wider queue scan has an explicit memory bound in addition to the HTTP
// deadline. Exceeding it is an error, never a successful truncated count.
const maxFeedInputBytes = 32 << 20

type PageResult struct {
	Body                         json.RawMessage
	Write                        previewcache.WriteReport
	CommitUnknown, CleanupFailed bool
}

func (r *PageResult) observe(value *Result) {
	if value == nil {
		return
	}
	r.Write.DirectoriesCreated = r.Write.DirectoriesCreated || value.Write.DirectoriesCreated
	r.Write.Attempted = r.Write.Attempted || value.Write.Attempted
	r.Write.Committed = r.Write.Committed || value.Write.Committed
	r.CommitUnknown = r.CommitUnknown || value.Write.Attempted && !value.Write.Committed
	r.CleanupFailed = r.CleanupFailed || value.CleanupFailed
}

// Page uses one writer gate and one current month. Normal SQL pagination stays
// page-local; only queue filtering/priority sorting needs the wider base set.
// Legacy summaries remain untouched and are never used as native decisions.
func (s *Service) Page(ctx context.Context, options discovery.PageOptions) (out *PageResult, err error) {
	out = &PageResult{}
	if s == nil || s.workspace == nil || s.now == nil || s.save == nil || ctx == nil {
		return out, fail("ASSESSMENT_SERVICE_INVALID")
	}
	if discovery.ValidatePage(options) != nil {
		return out, fail("ASSESSMENT_INPUT_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return out, fail("ASSESSMENT_INPUT_CANCELED")
	}
	defer release()
	now := s.now()
	month := assessmentcontext.Month{Year: now.Year(), Number: int(now.Month())}
	base := options
	base.Queues = nil
	wide := len(options.Queues) != 0 || options.Sort == "queue_priority"
	if options.Sort == "queue_priority" {
		base.Sort = "last_desc"
	}
	if wide {
		base.Offset, base.Limit = 0, 100
	}
	before, count, err := s.pageInputs(ctx, base, wide)
	if err != nil {
		return out, err
	}
	projected := slices.Clone(before)
	var contextWitness string
	for i := range projected {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		record := &projected[i]
		var result *Result
		if record.ContentScope == "confirmed_full" {
			result, err = s.assessOwned(ctx, record.DiscoveryID, month, true)
			out.observe(result)
			if err != nil {
				return out, err
			}
			witness := result.Preview.Receipt.Availability
			if identity := result.Preview.Identity; identity != nil {
				witness += "/" + identity.Candidate + "/" + identity.Context
			}
			if contextWitness != "" && contextWitness != witness {
				return out, fail("ASSESSMENT_REVIEW_INPUT_CHANGED")
			}
			contextWitness = witness
		}
		record.Decision = currentDecision(*record, result)
	}
	after, afterCount, err := s.pageInputs(ctx, base, wide)
	if err != nil {
		return out, err
	}
	if count != afterCount || !reflect.DeepEqual(before, after) {
		return out, fail("ASSESSMENT_REVIEW_INPUT_CHANGED")
	}
	if wide {
		projected = filterDecisions(projected, options.Queues)
		if options.Sort == "queue_priority" {
			// Base order is already last_seen DESC, ID ASC. Stable sort retains
			// that exact SQL tie order inside each native decision queue.
			slices.SortStableFunc(projected, func(a, b discovery.Record) int {
				return queueRank(a.Decision.Queue) - queueRank(b.Decision.Queue)
			})
		}
		count = int64(len(projected))
		start := min(options.Offset, count)
		end := start + min(int64(options.Limit), count-start)
		projected = projected[start:end]
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var next any
	if int64(len(projected)) < count-options.Offset {
		next = "offset:" + strconv.FormatInt(options.Offset+int64(len(projected)), 10)
	}
	raw, err := json.Marshal(map[string]any{"items": projected, "total": count, "next_cursor": next})
	if err != nil || len(raw) > 4<<20 {
		return out, fail("ASSESSMENT_REVIEW_RESULT_LIMIT")
	}
	out.Body = raw
	return out, nil
}

func (s *Service) pageInputs(ctx context.Context, base discovery.PageOptions, wide bool) ([]discovery.Record, int64, error) {
	count, err := s.workspace.DiscoveryCount(ctx, base.Filters)
	if err != nil {
		return nil, 0, err
	}
	items := []discovery.Record{}
	bytes := 0
	for {
		batch, err := s.workspace.DiscoveryFeedInputs(ctx, base)
		if err != nil {
			return nil, 0, err
		}
		raw, err := json.Marshal(batch)
		if err != nil || len(raw) > maxFeedInputBytes-bytes {
			return nil, 0, fail("ASSESSMENT_FEED_INPUT_LIMIT")
		}
		bytes += len(raw)
		items = append(items, batch...)
		if !wide || len(batch) < base.Limit {
			break
		}
		base.Offset += int64(len(batch))
	}
	if wide && int64(len(items)) != count {
		return nil, 0, fail("ASSESSMENT_REVIEW_INPUT_CHANGED")
	}
	return items, count, nil
}

func filterDecisions(items []discovery.Record, queues []string) []discovery.Record {
	if len(queues) == 0 {
		return items
	}
	filtered := make([]discovery.Record, 0, len(items))
	for _, record := range items {
		if slices.Contains(queues, record.Decision.Queue) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func queueRank(queue string) int {
	switch queue {
	case "clear_conflict":
		return 0
	case "review_first":
		return 1
	case "review":
		return 2
	default:
		return 3
	}
}

func currentDecision(record discovery.Record, result *Result) discovery.Decision {
	decision := discovery.Decision{Queue: "review", Label: "Review"}
	if record.ContentScope == "link_only" {
		decision.Queue, decision.Label = "needs_jd", "Needs JD"
		return decision
	}
	if record.ContentScope != "confirmed_full" {
		decision.Label = "Review capture"
		return decision
	}
	if result == nil || result.Preview == nil {
		return decision
	}
	receipt := result.Preview.Receipt
	if receipt.Availability != "available" || receipt.Scope != record.ContentScope {
		return decision
	}
	if !slices.Contains([]string{"clear_conflict", "no_clear_conflict", "review"}, receipt.Status) {
		return decision
	}
	decision.AssessmentAvailable = true
	switch receipt.Status {
	case "clear_conflict":
		decision.Queue, decision.Label = "clear_conflict", "Clear conflict"
		if len(receipt.Blockers) > 0 {
			decision.PrimaryEvidence = receipt.Blockers[0].Message
		}
	case "no_clear_conflict":
		decision.Queue, decision.Label = "review_first", "Review first"
	}
	if decision.PrimaryEvidence == "" && len(receipt.ReviewItems) > 0 {
		decision.PrimaryEvidence = receipt.ReviewItems[0].Text
	}
	decision.PrimaryEvidence = string([]rune(decision.PrimaryEvidence)[:min(len([]rune(decision.PrimaryEvidence)), 500)])
	if result.ComputedAt != "" {
		computed := result.ComputedAt
		decision.ComputedAt = &computed
	}
	return decision
}
