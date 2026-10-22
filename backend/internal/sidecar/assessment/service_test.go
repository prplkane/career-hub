package assessment

import (
	"context"
	"fmt"
	"testing"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/previewcache"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func requireFailure(t *testing.T, result *Result, err error, code string, committed bool) {
	t.Helper()
	typed, ok := err.(*Error)
	if !ok || typed.Code != code || result == nil || result.Preview != nil || result.Write.Committed != committed {
		t.Fatalf("expected %s, committed=%v; got %v / %v", code, committed, result, err)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
		if fmt.Sprintf(format, typed) != "Discovery assessment could not be completed." {
			t.Fatal("error exposed nonfixed details")
		}
	}
}

func TestServiceInputAndSharedWriterGate(t *testing.T) {
	month := assessmentcontext.Month{Year: 2026, Number: 9}
	s := &Service{workspace: &readonly.Workspace{}, now: time.Now, save: previewcache.SaveOwned}
	for _, test := range []struct {
		service *Service
		ctx     context.Context
		id      string
		month   assessmentcontext.Month
		code    string
	}{
		{nil, context.Background(), "synthetic", month, "ASSESSMENT_SERVICE_INVALID"},
		{s, nil, "synthetic", month, "ASSESSMENT_SERVICE_INVALID"},
		{s, context.Background(), "../unsafe", month, "ASSESSMENT_INPUT_INVALID"},
		{s, context.Background(), "synthetic", assessmentcontext.Month{}, "ASSESSMENT_INPUT_INVALID"},
	} {
		got, err := test.service.Assess(test.ctx, test.id, test.month)
		requireFailure(t, got, err, test.code, false)
	}
	// Both independent services must wait on the same gate before even trying
	// to open their intentionally nonexistent roots. No sleeps or filesystem.
	release, err := operationgate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := s.Assess(ctx, "synthetic", month)
	requireFailure(t, got, err, "ASSESSMENT_INPUT_CANCELED", false)
	writer := catalogwrite.New("not-a-root", "not-a-root")
	if got, err := writer.RenameCustomGroup(ctx, "custom", map[string]any{"name": "New"}, "stale"); got != nil || err == nil || err.Code != "CATALOG_OPERATION_CANCELLED" {
		t.Fatal("catalog writer did not honor cancellation before touching roots")
	}
	// Live requests must wait too, not just reject an already canceled input.
	// Without the shared gate each immediately fails on its nonexistent root.
	ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
	got, err = s.Assess(ctx, "synthetic", month)
	cancel()
	requireFailure(t, got, err, "ASSESSMENT_INPUT_CANCELED", false)
	ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if got, err := writer.RenameCustomGroup(ctx, "custom", map[string]any{"name": "New"}, "stale"); got != nil || err == nil || err.Code != "CATALOG_OPERATION_CANCELLED" {
		t.Fatal("catalog writer passed the held shared operation gate")
	}
}
