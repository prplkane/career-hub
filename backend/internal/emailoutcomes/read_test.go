package emailoutcomes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications"
)

func TestReadUpdatePageSourceFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "migration_email_application_updates.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Rows                []UpdateItem `json:"rows"`
		Total               int64        `json:"total"`
		ApplicationsUpdated int64        `json:"applications_updated"`
		Unlinked            int64        `json:"unlinked"`
		NeedsReview         int64        `json:"needs_review"`
		Pages               []struct {
			Offset     int64    `json:"offset"`
			Limit      int      `json:"limit"`
			IDs        []string `json:"ids"`
			NextCursor *string  `json:"next_cursor"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	db := outcomeDatabase(t)
	if _, err := db.Exec(`PRAGMA user_version=` + strconv.Itoa(applications.SchemaVersion)); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]UpdateItem)
	for _, row := range source.Rows {
		byID[row.OutcomeID] = row
		if _, err := db.Exec(`INSERT INTO job_email_application_outcomes
			(outcome_id,message_fingerprint,source_provider,posting_identity,source_url,
			title,company,location,application_status,evidence_code,observed_at,
			resolution,matched_discovery_id,matched_job_id,revision,created_at,updated_at)
			VALUES(?,'sha256:synthetic',?,'synthetic','https://example.test/',?,?,?,?,'fixture',?,?,NULLIF(?,''),NULLIF(?,''),?,'t0','t0')`,
			row.OutcomeID, row.SourceProvider, row.Title, row.Company, row.Location,
			row.ApplicationStatus, row.ObservedAt, row.Resolution,
			row.MatchedDiscoveryID, row.MatchedJobID, row.Revision); err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range source.Pages {
		got, problem := ReadUpdatePage(context.Background(), db, spec.Offset, spec.Limit)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		want := UpdatePage{Total: source.Total, NextCursor: spec.NextCursor,
			ApplicationsUpdated: source.ApplicationsUpdated, Unlinked: source.Unlinked,
			NeedsReview: source.NeedsReview, Items: []UpdateItem{}}
		for _, id := range spec.IDs {
			want.Items = append(want.Items, byID[id])
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Go page differs from Python source fixture at %d/%d: got=%+v want=%+v", spec.Offset, spec.Limit, got, want)
		}
	}
	for _, version := range []struct {
		value int
		code  string
	}{{applications.SchemaVersion - 1, "SCHEMA_PREPARATION_REQUIRED"}, {applications.SchemaVersion + 1, "SCHEMA_TOO_NEW"}} {
		if _, err := db.Exec(`PRAGMA user_version=` + strconv.Itoa(version.value)); err != nil {
			t.Fatal(err)
		}
		got, problem := ReadUpdatePage(context.Background(), db, 0, 2)
		if problem == nil || problem.Code != version.code || got.Total != 0 || got.Items != nil {
			t.Fatalf("schema %d leaked partial page: %+v %v", version.value, got, problem)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version=` + strconv.Itoa(applications.SchemaVersion)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE job_email_application_outcomes SET title=? WHERE outcome_id='email_outcome_c'`, strings.Repeat("x", 501)); err != nil {
		t.Fatal(err)
	}
	got, problem := ReadUpdatePage(context.Background(), db, 0, 2)
	if problem == nil || problem.Code != "VALUE_INVALID" || got.Items != nil {
		t.Fatalf("oversized stored field leaked partial page: %+v %v", got, problem)
	}
	if _, err := db.Exec(`UPDATE job_email_application_outcomes SET title='Analyst' WHERE outcome_id='email_outcome_c'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		offset int64
		limit  int
	}{{-1, 1}, {0, 0}, {0, 101}} {
		if _, problem := ReadUpdatePage(context.Background(), db, tc.offset, tc.limit); problem == nil || problem.Code != "INPUT_INVALID" {
			t.Fatal("invalid page input accepted", tc)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, problem := ReadUpdatePage(canceled, db, 0, 2); problem == nil || problem.Code != "CANCELED" {
		t.Fatal("canceled read reached database")
	}
}
