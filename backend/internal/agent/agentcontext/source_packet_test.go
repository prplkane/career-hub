package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSourcePacketUsesLibraryNotJobLedger(t *testing.T) {
	db := savedPacketDB(t)
	if _, e := db.Exec("DROP TABLE job_applications; DROP TABLE application_resume_attachments; DROP TABLE application_resume_plans"); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e := tx.Exec("PRAGMA query_only=ON"); e != nil {
		t.Fatal(e)
	}
	d, r := savedInputFixture()
	d.files[savedWorkbenchPath] = savedWorkbenchFixture
	source := SourceRequest{Text: "Software Engineer\nPython services"}
	raw, problem := PrepareSourcePacket(ctx, tx, d, r, SourcePacketRequest{Source: source, RegistryMode: "exact-auto", Profile: "sandbox", Channel: "preview"}, savedInputDate)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	if e := ValidateSourceResponse(ctx, raw, source, "exact-auto"); e != nil {
		t.Fatal(e.Code)
	}
	for path := range d.reads {
		if strings.HasPrefix(path, "jobs/") || strings.HasPrefix(path, "outputs/") || strings.HasPrefix(path, "intake_runs/") {
			t.Fatal("advisory packet read job/draft/history")
		}
	}
	if bytes.Contains(raw, []byte("private/path")) || bytes.Contains(raw, []byte("PRIVATE_RELATED_RESPONSE")) {
		t.Fatal("private metadata escaped")
	}
	var packet map[string]json.RawMessage
	_ = json.Unmarshal(raw, &packet)
	if string(packet["application"]) != "null" || !bytes.Contains(packet["resume_versions"], []byte("Alpha")) {
		t.Fatal("source packet lost library or gained an application")
	}
	var count int
	if tx.QueryRow("SELECT count(*) FROM resume_library").Scan(&count) != nil || count != 2 {
		t.Fatal("helper ended transaction or changed library")
	}
	if ValidateSourceResponse(ctx, raw, SourceRequest{Text: "other source"}, "exact-auto") == nil || ValidateSavedResponse(ctx, raw, "synthetic", "assessment", "exact-auto", nil) == nil {
		t.Fatal("wrong response intent accepted")
	}
}
