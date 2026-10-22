//go:build windows

package emaildiagnostics

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

func storeFixture(t *testing.T, home string) {
	t.Helper()
	ctx := context.Background()
	info, startupProblem := datahome.BootstrapForProcess(ctx, home, "dev")
	if startupProblem != nil {
		t.Fatal(startupProblem.Code)
	}
	root, pe := runtimepaths.Open(home)
	if pe != nil || root.RequireProcessOwnership() != nil {
		t.Fatalf("synthetic Home was not owned: %v", pe)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(home, "career_vault", "logs", "intake.jsonl")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte("{\"source\":\"python_reference\"}\n")
	if err := os.WriteFile(legacyPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if empty, problem := ReadPage(ctx, home, *info, "0"); problem != nil || empty.Cursor != "ne:000001:0" || len(empty.Events) != 0 {
		t.Fatalf("missing native journal was not an empty poll: %+v %v", empty, problem)
	}
	current := time.Date(2026, 9, 14, 2, 45, 0, 123456000, time.UTC)
	run, err := newWithClock("20260914T024500.123456Z_deadbeef", func() time.Time { return current },
		func([]byte) (int, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	started, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	wrong := *info
	wrong.DataHomeID = "dh_11111111111111111111111111111111"
	if out, problem := Append(ctx, home, wrong, started); out.Committed || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_HOME_CHANGED" {
		t.Fatalf("wrong Home wrote a native event: %+v %v", out, problem)
	}
	if page, problem := ReadPage(ctx, home, wrong, "0"); len(page.Events) != 0 || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_HOME_CHANGED" {
		t.Fatalf("wrong Home read a native page: %+v %v", page, problem)
	}
	if out, problem := Append(ctx, home, *info, Event{}); out.Committed || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_EVENT_INVALID" {
		t.Fatalf("unminted event was accepted: %+v %v", out, problem)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if out, problem := Append(canceled, home, *info, started); out.Committed || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_CANCELED" {
		t.Fatalf("canceled event was written: %+v %v", out, problem)
	}
	if page, problem := ReadPage(canceled, home, *info, "0"); len(page.Events) != 0 || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_CANCELED" {
		t.Fatalf("canceled reader returned a page: %+v %v", page, problem)
	}
	if out, problem := Append(ctx, home, *info, started); !out.Committed || problem != nil {
		t.Fatalf("first event not published: %+v %v", out, problem)
	}
	current = current.Add(time.Second)
	terminal, err := run.Failed("EMAIL_SYNC_AUTH_FAILED", "mailbox")
	if err != nil {
		t.Fatal(err)
	}
	if out, problem := Append(ctx, home, *info, terminal); !out.Committed || problem != nil {
		t.Fatalf("terminal event not published: %+v %v", out, problem)
	}
	segmentRoot := filepath.Join(home, filepath.FromSlash(segmentDirectory))
	firstPath := filepath.Join(segmentRoot, "segment-000001.jsonl")
	first, err := os.ReadFile(firstPath)
	if err != nil || !bytes.Equal(first, append(started.Bytes(), terminal.Bytes()...)) {
		t.Fatalf("native segment did not retain two exact events: %v", err)
	}
	if after, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(after, legacy) {
		t.Fatalf("native append touched Python log: %v", err)
	}
	page, problem := ReadPage(ctx, home, *info, "0")
	if problem != nil || len(page.Events) != 2 || page.HasMore ||
		page.Cursor != formatCursor(1, int64(len(first))) ||
		!bytes.Equal(page.Events[0], bytes.TrimSuffix(started.Bytes(), []byte{'\n'})) ||
		!bytes.Equal(page.Events[1], bytes.TrimSuffix(terminal.Bytes(), []byte{'\n'})) {
		t.Fatalf("native poll did not return two source-shaped events: %+v %v", page, problem)
	}
	if quiet, problem := ReadPage(ctx, home, *info, page.Cursor); problem != nil || quiet.Cursor != page.Cursor || len(quiet.Events) != 0 {
		t.Fatalf("EOF cursor was not stable: %+v %v", quiet, problem)
	}
	if invalid, problem := ReadPage(ctx, home, *info, "ne:000001:1"); len(invalid.Events) != 0 || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_CURSOR_INVALID" {
		t.Fatalf("mid-line cursor was accepted: %+v %v", invalid, problem)
	}
	// A full segment rolls without rewriting or truncating the older file.
	full := bytes.Repeat(started.Bytes(), int(runtimepaths.MaxReadBytes)/len(started.Bytes()))
	if err := os.WriteFile(firstPath, full, 0600); err != nil {
		t.Fatal(err)
	}
	if out, problem := Append(ctx, home, *info, terminal); !out.Committed || problem != nil {
		t.Fatalf("full segment did not rotate: %+v %v", out, problem)
	}
	second, err := os.ReadFile(filepath.Join(segmentRoot, "segment-000002.jsonl"))
	if err != nil || !bytes.Equal(second, terminal.Bytes()) {
		t.Fatalf("rotation did not publish one new segment: %v", err)
	}
	if unchanged, err := os.ReadFile(firstPath); err != nil || !bytes.Equal(unchanged, full) {
		t.Fatalf("rotation rewrote old segment: %v", err)
	}
	bounded, problem := ReadPage(ctx, home, *info, "0")
	if problem != nil || len(bounded.Events) != MaxPageEvents || !bounded.HasMore ||
		bounded.Cursor != formatCursor(1, int64(MaxPageEvents*len(started.Bytes()))) {
		t.Fatalf("large segment was not event-bounded: %+v %v", bounded, problem)
	}
	if next, problem := ReadPage(ctx, home, *info, bounded.Cursor); problem != nil || len(next.Events) != MaxPageEvents || !next.HasMore {
		t.Fatalf("large segment cursor did not advance: %+v %v", next, problem)
	}
	if rotated, problem := ReadPage(ctx, home, *info, "ne:000002:0"); problem != nil || len(rotated.Events) != 1 || rotated.HasMore ||
		!bytes.Equal(rotated.Events[0], bytes.TrimSuffix(terminal.Bytes(), []byte{'\n'})) {
		t.Fatalf("rotated segment was not readable: %+v %v", rotated, problem)
	}
	// Refuse hardlinked current segments before any replacement, including when
	// an unrelated path points at the same bytes.
	alias := filepath.Join(home, "synthetic-hardlink.jsonl")
	if err := os.Link(filepath.Join(segmentRoot, "segment-000002.jsonl"), alias); err != nil {
		t.Fatal(err)
	}
	if out, problem := Append(ctx, home, *info, started); out.Committed || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE" {
		t.Fatalf("hardlinked segment was changed: %+v %v", out, problem)
	}
	if after, err := os.ReadFile(alias); err != nil || !bytes.Equal(after, terminal.Bytes()) {
		t.Fatalf("hardlink target was changed: %v", err)
	}
	if leaked, problem := ReadPage(ctx, home, *info, "ne:000002:0"); len(leaked.Events) != 0 || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_READ_UNAVAILABLE" {
		t.Fatalf("hardlinked segment was readable: %+v %v", leaked, problem)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	for number := 3; number <= MaxDiagnosticSegments; number++ {
		name := filepath.Join(segmentRoot, formatSegmentName(number))
		if err := os.WriteFile(name, terminal.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Leave room for the start record but not the maximum terminal record.
	// Admission must refuse before touching the mailbox, not strand a run.
	nearFull := bytes.Repeat(started.Bytes(), int((runtimepaths.MaxReadBytes-MaxEventBytes/2)/int64(len(started.Bytes()))))
	if int64(len(nearFull)+len(started.Bytes())) > runtimepaths.MaxReadBytes ||
		int64(len(nearFull)+len(started.Bytes())+MaxEventBytes) <= runtimepaths.MaxReadBytes {
		t.Fatal("synthetic final segment did not exercise terminal reservation")
	}
	if err := os.WriteFile(filepath.Join(segmentRoot, formatSegmentName(MaxDiagnosticSegments)), nearFull, 0600); err != nil {
		t.Fatal(err)
	}
	if out, problem := Append(ctx, home, *info, started); out.Committed || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_LIMIT" {
		t.Fatalf("full eight-segment journal did not refuse a ninth: %+v %v", out, problem)
	}
	ninth := filepath.Join(segmentRoot, "segment-000009.jsonl")
	if err := os.WriteFile(ninth, terminal.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if leaked, problem := ReadPage(ctx, home, *info, "0"); len(leaked.Events) != 0 || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_LIMIT" {
		t.Fatalf("over-cap journal was read: %+v %v", leaked, problem)
	}
	if err := os.Remove(ninth); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(segmentRoot, "segment-000002.jsonl"), append(terminal.Bytes(), []byte("broken")...), 0600); err != nil {
		t.Fatal(err)
	}
	if leaked, problem := ReadPage(ctx, home, *info, "ne:000002:0"); len(leaked.Events) != 0 || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED" {
		t.Fatalf("incomplete native tail exposed a page: %+v %v", leaked, problem)
	}
	if err := os.Remove(firstPath); err != nil {
		t.Fatal(err)
	}
	if leaked, problem := ReadPage(ctx, home, *info, "0"); len(leaked.Events) != 0 || problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_DIRECTORY_INVALID" {
		t.Fatalf("missing rotated segment was silently skipped: %+v %v", leaked, problem)
	}
}

func TestNativeEmailDiagnosticStoreHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_EMAIL_DIAGNOSTIC_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	storeFixture(t, os.Getenv("CAREERHUB_EMAIL_DIAGNOSTIC_HOME"))
}

func TestNativeEmailDiagnosticStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeEmailDiagnosticStoreHelper$")
	command.Env = append(os.Environ(), "CAREERHUB_EMAIL_DIAGNOSTIC_HELPER=1",
		"CAREERHUB_EMAIL_DIAGNOSTIC_HOME="+filepath.Join(t.TempDir(), "home"))
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic native diagnostic store: %v %.1000s", err, output)
	}
}
