package applications

import (
	"context"
	"database/sql"
	"testing"
)

type countedReadQueries struct {
	queryer
	count int
}

func (q *countedReadQueries) QueryContext(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	q.count++
	return q.queryer.QueryContext(ctx, statement, args...)
}

func TestFullApplicationInputChecksPrecedeDatabaseUse(t *testing.T) {
	bad := "APPLIED"
	if result, err := ReadApplications(nil, nil, Filters{Status: &bad}); result != nil || err == nil || err.Code != "INVALID_STATUS" {
		t.Fatal("status validation changed")
	}
	if result, err := ReadApplications(nil, nil, Filters{Decision: &bad}); result != nil || err == nil || err.Code != "INVALID_DECISION" {
		t.Fatal("decision validation changed")
	}
	if result, err := ReadApplication(nil, nil, "\xff"); result != nil || err == nil || err.Code != "INPUT_UNSUPPORTED" {
		t.Fatal("invalid UTF8 reached database")
	}
	if result, err := ReadResumePlan(nil, nil, "\xff"); result != nil || err == nil || err.Code != "INPUT_UNSUPPORTED" {
		t.Fatal("invalid plan ID reached database")
	}
}
