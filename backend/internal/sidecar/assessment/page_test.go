package assessment

import (
	"context"
	"slices"
	"testing"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/discoverypreview"
	"careerhub.local/backend/internal/previewcache"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func TestCurrentDecisionRequiresVerifiedScopeAndCurrentContext(t *testing.T) {
	for _, status := range []string{"clear_conflict", "no_clear_conflict", "review"} {
		receipt := discoverypreview.Receipt{Scope: "confirmed_full", Availability: "available", Status: status,
			Blockers: []discoverypreview.Blocker{{Message: "Exact blocker"}}, ReviewItems: []discoverypreview.Review{{Text: "Exact review"}}}
		result := &Result{Preview: &readonly.DiscoveryPreview{Receipt: receipt}, ComputedAt: "2026-09-12T12:00:00Z"}
		for _, scope := range []string{"link_only", "selection", "page_visible", "confirmed_full"} {
			decision := currentDecision(discovery.Record{ContentScope: scope}, result)
			if scope != "confirmed_full" {
				if decision.AssessmentAvailable || decision.ComputedAt != nil || decision.PrimaryEvidence != "" {
					t.Fatal("unverified capture acquired a verdict", decision)
				}
				if scope == "link_only" && decision.Queue != "needs_jd" || scope != "link_only" && decision.Queue != "review" {
					t.Fatal(decision)
				}
				continue
			}
			want := map[string]string{"clear_conflict": "clear_conflict", "no_clear_conflict": "review_first", "review": "review"}[status]
			if decision.Queue != want || !decision.AssessmentAvailable || decision.ComputedAt == nil {
				t.Fatal(decision)
			}
		}
		result.Preview.Receipt.Availability = "context_unavailable"
		if got := currentDecision(discovery.Record{ContentScope: "confirmed_full"}, result); got.AssessmentAvailable || got.Queue != "review" {
			t.Fatal("missing context became a positive verdict")
		}
	}
}

func TestNativeDecisionFilterAndStablePriorityOrder(t *testing.T) {
	items := []discovery.Record{}
	for _, queue := range []string{"needs_jd", "review", "review_first", "clear_conflict", "review"} {
		items = append(items, discovery.Record{DiscoveryID: string(rune('a' + len(items))), Decision: discovery.Decision{Queue: queue}})
	}
	filtered := filterDecisions(items, []string{"review", "clear_conflict", "review"})
	if len(filtered) != 3 {
		t.Fatal("filter duplicated or dropped rows")
	}
	slices.SortStableFunc(filtered, func(a, b discovery.Record) int { return queueRank(a.Decision.Queue) - queueRank(b.Decision.Queue) })
	if filtered[0].DiscoveryID != "d" || filtered[1].DiscoveryID != "b" || filtered[2].DiscoveryID != "e" {
		t.Fatal("priority changed SQL tie order", filtered)
	}
}

func TestPageAggregationRetainsUnknownLaterPublication(t *testing.T) {
	page := &PageResult{}
	page.observe(&Result{Write: previewcache.WriteReport{Attempted: true, Committed: true}})
	page.observe(&Result{Write: previewcache.WriteReport{Attempted: true}, CleanupFailed: true})
	if !page.Write.Committed || !page.CommitUnknown || !page.CleanupFailed {
		t.Fatal("lost independently committed cache writes")
	}
	var service *Service
	if result, err := service.Page(context.Background(), discovery.PageOptions{}); result == nil || err == nil {
		t.Fatal("invalid service was accepted")
	}
}
