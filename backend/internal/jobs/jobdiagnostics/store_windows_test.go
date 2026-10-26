//go:build windows

package jobdiagnostics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const testJobDiagnosticHome = "CAREERHUB_TEST_JOB_DIAGNOSTIC_HOME"

func TestJobDiagnosticOwnedStore(t *testing.T) {
	if home := os.Getenv(testJobDiagnosticHome); home != "" {
		testOwnedStore(t, home)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJobDiagnosticOwnedStore$")
	cmd.Env = append(os.Environ(), testJobDiagnosticHome+"="+filepath.Join(t.TempDir(), "home"))
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fictional native job journal: %v %.1000s", err, output)
	}
}

func testOwnedStore(t *testing.T, home string) {
	t.Helper()
	ctx := context.Background()
	info, problem := datahome.BootstrapForProcess(ctx, home, "dev")
	if problem != nil {
		t.Fatal(problem.Code)
	}
	root, openErr := runtimepaths.Open(home)
	if openErr != nil || root.RequireProcessOwnership() != nil || root.Close() != nil {
		t.Fatalf("fictional Home not owned: %v", openErr)
	}
	legacyPath := filepath.Join(home, "career_vault", "logs", "intake.jsonl")
	legacy := []byte("{\"source\":\"python_reference\"}\n")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	run := fixedRun(t, "20260914T123000.123456Z_1234abcd")
	started, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	wrong := *info
	wrong.DataHomeID = "dh_11111111111111111111111111111111"
	if last, failure := ReadLastEvent(ctx, home, *info); failure != nil || last != nil {
		t.Fatalf("empty journal invented an event: %+v %v", last, failure)
	}
	if last, failure := ReadLastEvent(ctx, home, wrong); failure == nil || failure.Code != "JOB_DIAGNOSTIC_HOME_CHANGED" || last != nil {
		t.Fatalf("changed Home read the journal: %+v %v", last, failure)
	}
	if out, failure := Append(ctx, home, wrong, started); failure == nil || failure.Code != "JOB_DIAGNOSTIC_HOME_CHANGED" || out.Committed {
		t.Fatalf("wrong Home published an event: %+v %v", out, failure)
	}
	if out, failure := Append(ctx, home, *info, Event{}); failure == nil || failure.Code != "JOB_DIAGNOSTIC_EVENT_INVALID" || out.Committed {
		t.Fatalf("invalid event published: %+v %v", out, failure)
	}
	if out, failure := Append(ctx, home, *info, started); failure != nil || !out.Committed {
		t.Fatalf("started event not published: %+v %v", out, failure)
	}
	if last, failure := ReadLastEvent(ctx, home, *info); failure != nil || last == nil || last.Event != "started" || last.RunID != run.ID() {
		t.Fatalf("in-flight start lost: %+v %v", last, failure)
	}
	other := fixedRun(t, "20260914T123000.123456Z_1234abce")
	otherStart, err := other.Started()
	if err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, otherStart); failure == nil || failure.Code != "JOB_DIAGNOSTIC_SEQUENCE_INVALID" || out.Committed {
		t.Fatalf("interleaved run was admitted: %+v %v", out, failure)
	}
	prior, next := sha256.Sum256([]byte("fictional prior")), sha256.Sum256([]byte("fictional next"))
	plan, err := run.Planned(prior, next)
	if err != nil {
		t.Fatal(err)
	}
	witness := decodedEvent(t, plan)
	cardPath := filepath.Join(home, "jobs", "normalized", "fictional-job.yaml")
	if observed, failure := ObservePlannedPublication(ctx, home, *info, witness); failure != nil || observed != PublicationUnknown {
		t.Fatalf("missing card invented publication: %s %v", observed, failure)
	}
	if err := os.MkdirAll(filepath.Dir(cardPath), 0700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		content []byte
		want    PublicationState
	}{{[]byte("fictional prior"), PublicationNotPublished}, {[]byte("fictional next"), PublicationPublished}, {[]byte("third card"), PublicationUnknown}} {
		if err := os.WriteFile(cardPath, test.content, 0600); err != nil {
			t.Fatal(err)
		}
		if observed, failure := ObservePlannedPublication(ctx, home, *info, witness); failure != nil || observed != test.want {
			t.Fatalf("guarded card classification: %s, want %s: %v", observed, test.want, failure)
		}
	}
	if observed, failure := ObservePlannedPublication(ctx, home, wrong, witness); failure == nil || failure.Code != "JOB_DIAGNOSTIC_HOME_CHANGED" || observed != PublicationUnknown {
		t.Fatalf("changed Home identified a card: %s %v", observed, failure)
	}
	cardAlias := filepath.Join(home, "jobs", "normalized", "other.yaml")
	if err := os.Link(cardPath, cardAlias); err != nil {
		t.Fatal(err)
	}
	if observed, failure := ObservePlannedPublication(ctx, home, *info, witness); failure == nil || failure.Code != "JOB_DIAGNOSTIC_CARD_UNAVAILABLE" || observed != PublicationUnknown {
		t.Fatalf("hardlinked card was classified: %s %v", observed, failure)
	}
	if err := os.Remove(cardAlias); err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, plan); failure != nil || !out.Committed {
		t.Fatalf("planned witness not published: %+v %v", out, failure)
	}
	if last, failure := ReadLastEvent(ctx, home, *info); failure != nil || last == nil || last.Event != "planned" || last.NextCardSHA256 != next {
		t.Fatalf("in-flight plan lost: %+v %v", last, failure)
	}
	terminal, err := run.Failed("JOB_REANALYSIS_COMMITTED_UNVERIFIED", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, terminal); failure != nil || !out.Committed {
		t.Fatalf("terminal publication facts not retained: %+v %v", out, failure)
	}
	if last, failure := ReadLastEvent(ctx, home, *info); failure != nil || last == nil || last.Event != "failed" || !last.Committed {
		t.Fatalf("terminal publication facts lost: %+v %v", last, failure)
	}
	segmentPath := filepath.Join(home, filepath.FromSlash(segmentDirectory), formatSegmentName(1))
	stored, err := os.ReadFile(segmentPath)
	if err != nil || !bytes.Equal(stored, append(append(started.Bytes(), plan.Bytes()...), terminal.Bytes()...)) {
		t.Fatalf("journal did not retain exact ordered events: %v", err)
	}
	if unchanged, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(unchanged, legacy) {
		t.Fatalf("job journal touched Python log: %v", err)
	}
	page, readProblem := ReadPage(ctx, home, *info, "0")
	if readProblem != nil || len(page.Events) != 3 || page.HasMore || page.Cursor != formatCursor(1, int64(len(stored))) {
		t.Fatalf("owned journal read lost events: %+v %v", page, readProblem)
	}
	if quiet, failure := ReadPage(ctx, home, *info, page.Cursor); failure != nil || len(quiet.Events) != 0 || quiet.Cursor != page.Cursor {
		t.Fatalf("EOF cursor was unstable: %+v %v", quiet, failure)
	}
	if _, failure := ReadPage(ctx, home, *info, "nj:000001:1"); failure == nil || failure.Code != "JOB_DIAGNOSTIC_CURSOR_INVALID" {
		t.Fatalf("mid-line cursor was accepted: %v", failure)
	}
	if _, failure := ReadPage(ctx, home, wrong, "0"); failure == nil || failure.Code != "JOB_DIAGNOSTIC_HOME_CHANGED" {
		t.Fatalf("wrong Home read journal: %v", failure)
	}
	noOp := fixedRun(t, "20260914T123000.123456Z_1234abca")
	noOpStart, err := noOp.Started()
	if err != nil {
		t.Fatal(err)
	}
	noOpEnd, err := noOp.Unchanged()
	if err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, noOpStart); failure != nil || !out.Committed {
		t.Fatalf("no-op start not published: %+v %v", out, failure)
	}
	if out, failure := Append(ctx, home, *info, noOpEnd); failure != nil || !out.Committed {
		t.Fatalf("no-op completion not published: %+v %v", out, failure)
	}
	noOpPage, failure := ReadPage(ctx, home, *info, page.Cursor)
	if failure != nil || len(noOpPage.Events) != 2 || decodedEvent(t, Event{raw: append(bytes.Clone(noOpPage.Events[1]), '\n')}).Committed {
		t.Fatalf("no-op journal events not readable: %+v %v", noOpPage, failure)
	}
	if out, failure := Append(ctx, home, *info, otherStart); failure != nil || !out.Committed {
		t.Fatalf("next run could not start: %+v %v", out, failure)
	}
	alias := filepath.Join(home, "fictional-hardlink.jsonl")
	if err := os.Link(segmentPath, alias); err != nil {
		t.Fatal(err)
	}
	otherFailure, err := Interrupted(decodedEvent(t, otherStart), PublicationNotPublished)
	if err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, otherFailure); failure == nil || failure.Code != "JOB_DIAGNOSTIC_STORE_UNAVAILABLE" || out.Committed {
		t.Fatalf("hardlinked segment was edited: %+v %v", out, failure)
	}
	if _, failure := ReadPage(ctx, home, *info, "0"); failure == nil || failure.Code != "JOB_DIAGNOSTIC_READ_UNAVAILABLE" {
		t.Fatalf("hardlinked segment was read: %v", failure)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, otherFailure); failure != nil || !out.Committed {
		t.Fatalf("interrupted run could not be closed: %+v %v", out, failure)
	}
	intake := fixedIntakeRun(t, "20260920T154500.123456Z_4321dcba", "fictional mixed-store source", "captured")
	intakeStart, err := intake.Started()
	if err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, intakeStart); failure != nil || !out.Committed {
		t.Fatalf("intake start not published after reanalysis: %+v %v", out, failure)
	}
	intakeDispatch, err := intake.Dispatched()
	if err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, intakeDispatch); failure != nil || !out.Committed {
		t.Fatalf("intake dispatch not published: %+v %v", out, failure)
	}
	intakeDone, err := intake.Completed("terminal", false)
	if err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, intakeDone); failure != nil || !out.Committed {
		t.Fatalf("intake terminal not published: %+v %v", out, failure)
	}
	if last, failure := ReadLastEvent(ctx, home, *info); failure != nil || last == nil || last.Operation != "intake" || last.DurablePhase != "terminal" {
		t.Fatalf("mixed journal lost intake terminal: %+v %v", last, failure)
	}
	mixed, failure := ReadPage(ctx, home, *info, noOpPage.Cursor)
	if failure != nil || len(mixed.Events) != 5 {
		t.Fatalf("mixed reanalysis/intake page invalid: %+v %v", mixed, failure)
	}
	lastMixed := decodedEvent(t, Event{raw: append(bytes.Clone(mixed.Events[4]), '\n')})
	if lastMixed.Operation != "intake" || lastMixed.SourceKeySHA256 == ([32]byte{}) {
		t.Fatal("mixed page lost content-minimized intake identity", lastMixed)
	}
	third := fixedRun(t, "20260914T123000.123456Z_1234abcf")
	thirdStart, err := third.Started()
	if err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, thirdStart); failure != nil || !out.Committed {
		t.Fatalf("recovered journal blocked a new run: %+v %v", out, failure)
	}
	if err := os.WriteFile(segmentPath, append(stored, []byte("broken")...), 0600); err != nil {
		t.Fatal(err)
	}
	if out, failure := Append(ctx, home, *info, started); failure == nil || failure.Code != "JOB_DIAGNOSTIC_RECOVERY_REQUIRED" || out.Committed {
		t.Fatalf("damaged tail was silently extended: %+v %v", out, failure)
	}
	if _, failure := ReadPage(ctx, home, *info, "0"); failure == nil || failure.Code != "JOB_DIAGNOSTIC_RECOVERY_REQUIRED" {
		t.Fatalf("damaged tail exposed a page: %v", failure)
	}
	if last, failure := ReadLastEvent(ctx, home, *info); failure == nil || failure.Code != "JOB_DIAGNOSTIC_RECOVERY_REQUIRED" || last != nil {
		t.Fatalf("damaged tail became empty recovery state: %+v %v", last, failure)
	}
	var many bytes.Buffer
	for i := 0; i < 129; i++ {
		entry := fixedRun(t, fmt.Sprintf("20260914T123000.123456Z_%08x", i+1))
		begin, beginErr := entry.Started()
		failed, failedErr := entry.Failed("JOB_REANALYSIS_PREPARATION_FAILED", false, false)
		if beginErr != nil || failedErr != nil {
			t.Fatalf("fictional event build: %v %v", beginErr, failedErr)
		}
		many.Write(begin.Bytes())
		many.Write(failed.Bytes())
	}
	if err := os.WriteFile(segmentPath, many.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	firstPage, failure := ReadPage(ctx, home, *info, "0")
	if failure != nil || len(firstPage.Events) != MaxPageEvents || !firstPage.HasMore {
		t.Fatalf("journal page was not bounded: %+v %v", firstPage, failure)
	}
	secondPage, failure := ReadPage(ctx, home, *info, firstPage.Cursor)
	if failure != nil || len(secondPage.Events) != MaxPageEvents || !secondPage.HasMore {
		t.Fatalf("bounded cursor did not advance: %+v %v", secondPage, failure)
	}
	lastPage, failure := ReadPage(ctx, home, *info, secondPage.Cursor)
	if failure != nil || len(lastPage.Events) != 2 || lastPage.HasMore {
		t.Fatalf("bounded reader lost tail events: %+v %v", lastPage, failure)
	}
}
