//go:build windows

package diagnosticslog

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emaildiagnostics"
	"careerhub.local/backend/internal/jobdiagnostics"
)

func fixture(t *testing.T, home string) (datahome.Info, string) {
	t.Helper()
	info, problem := datahome.BootstrapForProcess(context.Background(), home, "dev")
	if problem != nil {
		t.Fatal(problem)
	}
	path := filepath.Join(home, "career_vault", "logs", "intake.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	return *info, path
}

func legacyThenNativeWithPartialTail(t *testing.T, home string, info datahome.Info, path string) {
	legacy := []byte("{\"run_id\":\"old\",\"status\":\"success\"}\n{\"run_id\":\"partial\"")
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	run, err := emaildiagnostics.New("20260914T024500.123456Z_deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	event, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	if out, problem := emaildiagnostics.Append(context.Background(), home, info, event); problem != nil || !out.Committed {
		t.Fatalf("native fixture append: %+v %v", out, problem)
	}
	jobRun, err := jobdiagnostics.New("fictional-job")
	if err != nil {
		t.Fatal(err)
	}
	jobEvent, err := jobRun.Started()
	if err != nil {
		t.Fatal(err)
	}
	if out, problem := jobdiagnostics.Append(context.Background(), home, info, jobEvent); problem != nil || !out.Committed {
		t.Fatalf("native job fixture append: %+v %v", out, problem)
	}
	first, problem := ReadPage(context.Background(), home, info, "0")
	if problem != nil || len(first.Events) != 1 || !first.HasMore || first.Cursor != "dl3:36:ne:000001:0:nj:000001:0:"+initialIntakeCursor() {
		t.Fatalf("legacy page: %+v %v", first, problem)
	}
	second, problem := ReadPage(context.Background(), home, info, first.Cursor)
	if problem != nil || len(second.Events) != 1 || !second.HasMore || !strings.Contains(string(second.Events[0]), "gmail_sync") {
		t.Fatalf("native page after partial legacy line: %+v %v", second, problem)
	}
	third, problem := ReadPage(context.Background(), home, info, second.Cursor)
	if problem != nil || len(third.Events) != 1 || !third.HasMore || !strings.Contains(string(third.Events[0]), "job_reanalysis_run") {
		t.Fatalf("native job page after email: %+v %v", third, problem)
	}
	upgraded, problem := ReadPage(context.Background(), home, info, "dl1:36:ne:000001:0")
	if problem != nil || len(upgraded.Events) != 1 || !strings.HasPrefix(upgraded.Cursor, "dl3:") {
		t.Fatalf("old cursor was not upgraded: %+v %v", upgraded, problem)
	}
	if err := os.WriteFile(path, append(legacy, []byte("}\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	fourth, problem := ReadPage(context.Background(), home, info, third.Cursor)
	if problem != nil || len(fourth.Events) != 1 || !fourth.HasMore || !strings.Contains(string(fourth.Events[0]), "partial") {
		t.Fatalf("completed legacy tail: %+v %v", fourth, problem)
	}
	var value map[string]any
	if json.Unmarshal(fourth.Events[0], &value) != nil || value["run_id"] != "partial" {
		t.Fatalf("invalid completed legacy event: %s", fourth.Events[0])
	}
}

func legacyCorruptionAndCursorExpiry(t *testing.T, home string, info datahome.Info, path string) {
	for _, content := range []string{"{\"run_id\":\"old\"}\nnot-json\n", "{\"run_id\":\"old\"}\n[]\n", "{\"run_id\":\"old\"}\n{\"bad\":\"\xff\"}\n"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		page, problem := ReadPage(context.Background(), home, info, "0")
		if len(page.Events) != 0 || problem == nil || problem.Code != "DIAGNOSTIC_LEGACY_CORRUPT" {
			t.Fatalf("corrupt legacy page leaked events: %+v %v", page, problem)
		}
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"dl1:4:ne:000001:0", "dl1:1:ne:000001:0"} {
		page, problem := ReadPage(context.Background(), home, info, cursor)
		if len(page.Events) != 0 || problem == nil {
			t.Fatalf("invalid/stale boundary accepted: %q %+v %v", cursor, page, problem)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	page, problem := ReadPage(context.Background(), home, info, "dl1:3:ne:000001:0")
	if len(page.Events) != 0 || problem == nil || problem.Code != "DIAGNOSTIC_CURSOR_EXPIRED" {
		t.Fatalf("deleted legacy file reset cursor: %+v %v", page, problem)
	}
}

func legacyOversizedLineAndHardlink(t *testing.T, home string, info datahome.Info, path string) {
	if err := os.WriteFile(path, []byte("{\"content\":\""+strings.Repeat("x", 256<<10)+"\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	page, problem := ReadPage(context.Background(), home, info, "0")
	if len(page.Events) != 0 || problem == nil || problem.Code != "DIAGNOSTIC_LEGACY_LINE_TOO_LARGE" {
		t.Fatalf("oversized line accepted: %+v %v", page, problem)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(home, "alias.jsonl")); err != nil {
		t.Fatal(err)
	}
	page, problem = ReadPage(context.Background(), home, info, "0")
	if len(page.Events) != 0 || problem == nil || problem.Code != "DIAGNOSTIC_LEGACY_UNAVAILABLE" {
		t.Fatalf("hardlinked legacy file exposed events: %+v %v", page, problem)
	}
}

func nativeIntakeLifecycle(t *testing.T, home string, info datahome.Info) {
	directory := filepath.Join(home, "career_vault", "logs", "native-intake")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 9, 20, 19, 0, 0, 0, time.UTC)
	write := func(operation, state, stage, code string, modified time.Time) {
		t.Helper()
		raw, err := json.Marshal(nativeIntakeRecord{SchemaVersion: 1, OperationID: operation,
			SourceID: "src_fictional_resume_digest_1234567890", State: state, Stage: stage, ErrorCode: code})
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(directory, operation+".json")
		if err := os.WriteFile(name, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	firstID := "11111111111111111111111111111111"
	secondID := "22222222222222222222222222222222"
	write(firstID, "success", "completed", "", stamp)
	first, problem := readNativeIntake(context.Background(), home, info, initialIntakeCursor())
	if problem != nil || len(first.events) != 1 || first.hasMore || !strings.Contains(string(first.events[0]), `"status":"success"`) || strings.Contains(string(first.events[0]), "src_fictional") {
		t.Fatalf("native success projection: %+v %v %s", first, problem, first.events)
	}
	write(secondID, "publishing", "publication", "", stamp.Add(time.Second))
	second, problem := readNativeIntake(context.Background(), home, info, first.cursor)
	if problem != nil || len(second.events) != 1 || !strings.Contains(string(second.events[0]), `"status":"running"`) {
		t.Fatalf("native append projection: %+v %v %s", second, problem, second.events)
	}
	write(secondID, "success", "completed", "", stamp.Add(2*time.Second))
	terminal, problem := readNativeIntake(context.Background(), home, info, second.cursor)
	if problem != nil || len(terminal.events) != 2 || !strings.Contains(string(terminal.events[1]), `"status":"success"`) {
		t.Fatalf("native terminal replacement was not revisited: %+v %v %s", terminal, problem, terminal.events)
	}
}

func TestMergedDiagnosticsHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_MERGED_DIAGNOSTICS_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	home := os.Getenv("CAREERHUB_MERGED_DIAGNOSTICS_HOME")
	info, path := fixture(t, home)
	t.Run("legacy then native", func(t *testing.T) { legacyThenNativeWithPartialTail(t, home, info, path) })
	t.Run("corruption and expiry", func(t *testing.T) { legacyCorruptionAndCursorExpiry(t, home, info, path) })
	t.Run("oversized and hardlink", func(t *testing.T) { legacyOversizedLineAndHardlink(t, home, info, path) })
	t.Run("resume digest diagnostics", func(t *testing.T) { nativeIntakeLifecycle(t, home, info) })
}

func TestMergedDiagnostics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMergedDiagnosticsHelper$")
	command.Env = append(os.Environ(), "CAREERHUB_MERGED_DIAGNOSTICS_HELPER=1",
		"CAREERHUB_MERGED_DIAGNOSTICS_HOME="+filepath.Join(t.TempDir(), "home"))
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("merged diagnostics fixture: %v %.1600s", err, output)
	}
}
