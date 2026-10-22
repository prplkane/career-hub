package emailimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/emailcandidates"
	"careerhub.local/backend/internal/emailmime"
)

func fileEntry(name string, content []byte) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"file_name": name, "content_base64": base64.StdEncoding.EncodeToString(content)})
	return raw
}

func TestBatchMIMEBudgetErrorIsPublicAndContentFree(t *testing.T) {
	got := batchFileError(string(emailmime.ErrParseBudget))
	if got.Code != "EMAIL_MIME_BYTES_LIMIT_EXCEEDED" || got.Message != "Email structure exceeds the supported parsing budget." {
		t.Fatalf("budget error lost public mapping: %+v", got)
	}
}

func TestBatchMixedStatusAndSuggestionsStaySeparate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "email_alerts", "handshake_application_sent_with_suggestions.eml"))
	if err != nil {
		t.Fatal(err)
	}
	batch, problem := PrepareFiles([]json.RawMessage{fileEntry("mixed.eml", raw)})
	if problem != nil {
		t.Fatal(problem)
	}
	writer := batchWriter()
	writer.outcome = func(candidate emailcandidates.ApplicationOutcome) (*applicationwrite.Outcome, *applicationwrite.Error) {
		if candidate.Company != "Synthetic AI" || candidate.ApplicationStatus != "applied" {
			t.Fatal("wrong application signal", candidate)
		}
		return ownedJSON(map[string]any{"outcome_id": "unlinked_1", "resolution": "unmatched", "private_extra": "not-returned"}, true), nil
	}
	out, problem := batch.Run(context.Background(), writer)
	if problem != nil || out.Result.Summary["application_outcomes_unlinked"] != 1 || out.Result.Summary["discoveries_created"] != 3 || out.Result.Summary["applications_updated"] != 0 {
		t.Fatalf("result=%+v error=%v", out, problem)
	}
	if strings.Join(writer.calls, ",") != "outcome,candidate,candidate,candidate" {
		t.Fatal(writer.calls)
	}
	if _, exists := out.Result.Files[0].ApplicationOutcomes[0]["private_extra"]; exists {
		t.Fatal("unlisted owner field escaped projection")
	}
}

func TestBatchNoJobsAndInvalidEntriesDoNotWrite(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "email_alerts", "no_job_links.eml"))
	if err != nil {
		t.Fatal(err)
	}
	batch, problem := PrepareFiles([]json.RawMessage{fileEntry("none.eml", raw), json.RawMessage(`null`), json.RawMessage(`{"file_name":true,"content_base64":"YQ=="}`), json.RawMessage(`{"file_name":"x.eml","content_base64":"YQ==\n"}`)})
	if problem != nil {
		t.Fatal(problem)
	}
	writer := batchWriter()
	out, problem := batch.Run(context.Background(), writer)
	if problem != nil || out.Committed || len(writer.calls) != 0 || out.Result.Summary["files_without_candidates"] != 1 || out.Result.Summary["files_failed"] != 3 {
		t.Fatalf("result=%+v error=%v", out, problem)
	}
}
func batchFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "email_alerts", "capture_quality_drift.eml"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func batchWriter() *fakeWriter {
	return &fakeWriter{capture: func(request discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error) {
		card := map[string]any{"discovery_id": "dsc_fixture", "state": "inbox", "needs_content": true}
		for _, key := range []string{"title", "company", "location", "observed_url", "posting_identity", "content_scope"} {
			card[key] = request[key]
		}
		return ownedJSON(map[string]any{"outcome": "created", "discovery": card}, true), nil
	}}
}
func TestBatchFilesPreserveProjectionOrderAndSafeErrors(t *testing.T) {
	batch, err := PrepareFiles([]json.RawMessage{fileEntry(" first.eml ", batchFixture(t)), json.RawMessage(`{"file_name":"bad.eml","content_base64":"!!!"}`), json.RawMessage(`{"file_name":"../bad.eml","content_base64":"YQ=="}`)})
	if err != nil {
		t.Fatal(err)
	}
	writer := batchWriter()
	out, problem := batch.Run(context.Background(), writer)
	if problem != nil || !out.Committed || out.CommitUnknown {
		t.Fatalf("out=%+v err=%v", out, problem)
	}
	if len(writer.calls) != 1 || writer.captured[0]["source_channel"] != ChannelFile {
		t.Fatal("wrong writer or source channel")
	}
	if out.Result.Summary["discoveries_created"] != 1 || out.Result.Summary["files_failed"] != 2 || out.Result.Files[0].FileName != "first.eml" {
		t.Fatalf("result=%+v", out.Result)
	}
	if out.Result.Files[1].Error.Code != "EMAIL_IMPORT_DECODE_FAILED" || out.Result.Files[2].Error.Code != "EMAIL_IMPORT_FILE_INVALID" {
		t.Fatal("file errors changed")
	}
}
func TestBatchRejectsCountsAndTotalBeforeWrites(t *testing.T) {
	for _, entries := range [][]json.RawMessage{nil, make([]json.RawMessage, 11)} {
		if _, e := PrepareFiles(entries); e == nil || e.Code != "EMAIL_IMPORT_FILE_COUNT_INVALID" {
			t.Fatal(e)
		}
	}
	entry := fileEntry("large.eml", []byte(strings.Repeat("x", 9*1024*1024)))
	if _, e := PrepareFiles([]json.RawMessage{entry, entry, entry}); e == nil || e.Code != "EMAIL_IMPORT_TOTAL_TOO_LARGE" {
		t.Fatal(e)
	}
	oversize := fileEntry("large.eml", make([]byte, emailmime.MaxEmailBytes+1))
	batch, e := PrepareFiles([]json.RawMessage{oversize})
	if e != nil || batch.files[0].result.Error.Code != "EMAIL_IMPORT_FILE_TOO_LARGE" {
		t.Fatal(e)
	}
}
func TestBatchKnownFailurePartialAndUncertaintyStops(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "known", true: "uncertain"}[uncertain], func(t *testing.T) {
			entry := fileEntry("jobs.eml", batchFixture(t))
			batch, _ := PrepareFiles([]json.RawMessage{entry, entry, entry})
			writer := batchWriter()
			succeed := writer.capture
			writer.capture = func(request discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error) {
				if len(writer.calls) == 2 {
					return &applicationwrite.Outcome{CommitAttempted: uncertain}, &applicationwrite.Error{Code: "private-error-must-not-leak"}
				}
				return succeed(request)
			}
			out, problem := batch.Run(context.Background(), writer)
			if !out.Committed {
				t.Fatal("prior commit lost")
			}
			if uncertain {
				if problem == nil || problem.Code != "EMAIL_IMPORT_WRITE_UNVERIFIED" || !out.CommitUnknown || len(writer.calls) != 2 {
					t.Fatalf("out=%+v err=%v calls=%v", out, problem, writer.calls)
				}
			} else {
				if problem != nil || out.Result.Summary["files_failed"] != 1 || out.Result.Summary["discoveries_created"] != 2 {
					t.Fatalf("out=%+v err=%v", out, problem)
				}
				raw, _ := json.Marshal(out.Result)
				if strings.Contains(string(raw), "private-error") {
					t.Fatal("owner error leaked")
				}
			}
		})
	}
}
func TestBatchCancellationAndMalformedCommittedResult(t *testing.T) {
	batch, _ := PrepareFiles([]json.RawMessage{fileEntry("jobs.eml", batchFixture(t))})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer := batchWriter()
	if _, problem := batch.Run(ctx, writer); problem == nil || problem.Code != "EMAIL_IMPORT_CANCELED" || len(writer.calls) != 0 {
		t.Fatal(problem)
	}
	writer.capture = func(discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error) {
		return ownedJSON(map[string]any{"outcome": "created"}, true), nil
	}
	out, problem := batch.Run(context.Background(), writer)
	if problem == nil || !out.Committed || !out.CommitUnknown {
		t.Fatal("malformed committed result must not be success")
	}
}
