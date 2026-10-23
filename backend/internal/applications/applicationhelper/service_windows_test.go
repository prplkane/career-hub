//go:build windows

package applicationhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const marker = `{"application":"CareerHub","channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef","format_version":2}`

type nativeRequest struct {
	Root string
	Case oracleCase
	Mode string
}
type nativeOutput struct {
	Outcome *Outcome
	Error   *Error
	Files   map[string]*string
}

func writeFixture(t *testing.T, root, path string, raw []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if e := os.MkdirAll(filepath.Dir(full), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T, c oracleCase) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	if e := os.MkdirAll(filepath.Join(root, "career_vault"), 0700); e != nil {
		t.Fatal(e)
	}
	for path, raw := range c.Files {
		if raw != nil {
			writeFixture(t, root, path, []byte(*raw))
		}
	}
	writeFixture(t, root, datahome.MarkerPath, []byte(marker))
	return root
}
func settingsFiles(t *testing.T, root string) map[string]*string {
	t.Helper()
	result := map[string]*string{}
	for _, path := range []string{ConfigurationPath, DemographicsPath, AnswersPath} {
		raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if os.IsNotExist(e) {
			result[path] = nil
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		text := string(raw)
		result[path] = &text
	}
	return result
}
func expectedRevision(t *testing.T, root, operation string) string {
	t.Helper()
	path := ConfigurationPath
	if strings.HasPrefix(operation, "demographics.") {
		path = DemographicsPath
	}
	if strings.HasPrefix(operation, "answers.") {
		path = AnswersPath
	}
	raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	present := e == nil
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if path == ConfigurationPath {
		_, content, e := configuration(raw, present)
		if e == nil {
			return revision(content)
		}
	}
	if !present {
		if path == DemographicsPath {
			raw = demographicsBytes(nil)
		} else if path == AnswersPath {
			raw = []byte("schema_version: 1\nanswers: []\n")
		}
	}
	return revision(raw)
}
func TestHelperNativeProcess(t *testing.T) {
	if os.Getenv("CAREERHUB_HELPER_NATIVE") != "1" {
		t.Skip("child process only")
	}
	var r nativeRequest
	if e := json.NewDecoder(os.Stdin).Decode(&r); e != nil {
		t.Fatal(e)
	}
	root, e := runtimepaths.Open(r.Root)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if r.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	s := New(r.Root)
	counter := 0
	s.id = func() (string, error) { counter++; return fmt.Sprintf("answer_%032x", counter), nil }
	s.now = func() time.Time { return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if r.Mode == "pending" {
		writeFixture(t, r.Root, datahome.ResumeRecoveryPath, []byte("{}"))
	}
	if r.Mode == "canceled" {
		cancel()
	}
	if r.Mode == "close-failure" {
		s.close = func(root *runtimepaths.Root) error { _ = root.Close(); return errors.New("private native close cause") }
	}
	if r.Mode == "create-failure" {
		s.create = func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "FILE_SYNC"}
		}
	}
	if r.Mode == "replace-failure" {
		s.replace = func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "FILE_SYNC"}
		}
	}
	if r.Mode == "ambiguous-create" {
		s.create = func(root *runtimepaths.Root, path string, raw []byte) (bool, *runtimepaths.Error) {
			done, e := root.CreateFile(path, raw)
			if e != nil {
				return done, e
			}
			return done, &runtimepaths.Error{Code: "FILE_RENAME"}
		}
	}
	if r.Mode == "ambiguous-replace" {
		s.replace = func(root *runtimepaths.Root, path string, old, raw []byte) (bool, *runtimepaths.Error) {
			done, e := root.ReplaceFile(path, old, raw)
			if e != nil {
				return done, e
			}
			return done, &runtimepaths.Error{Code: "FILE_RENAME"}
		}
	}
	s.checkpoint = func(stage string) *Error {
		if r.Mode == "fail:"+stage {
			return failure(stage, "SYNTHETIC_FAILURE")
		}
		if r.Mode == "crash:"+stage {
			os.Exit(86)
		}
		if r.Mode == "cancel:"+stage {
			cancel()
		}
		if stage == "after_read" && strings.HasPrefix(r.Mode, "drift:") {
			path := strings.TrimPrefix(r.Mode, "drift:")
			if path != ConfigurationPath && path != DemographicsPath && path != AnswersPath && path != "career_vault/profile.yaml" && path != datahome.MarkerPath {
				t.Fatal("fixture drift path")
			}
			if path == datahome.MarkerPath {
				writeFixture(t, r.Root, path, []byte(strings.Replace(marker, "0123456789abcdef0123456789abcdef", "abcdef0123456789abcdef0123456789", 1)))
			} else {
				raw, _ := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(path)))
				writeFixture(t, r.Root, path, append([]byte("# intervening edit\n"), raw...))
			}
		}
		if stage == "before_publish" && r.Mode == "intervening-create" {
			writeFixture(t, r.Root, ConfigurationPath, []byte("schema_version: 2\nenabled_fields: [phone]\n"))
		}
		if stage == "after_publish" && r.Mode == "third-state" {
			writeFixture(t, r.Root, ConfigurationPath, []byte("schema_version: 2\nenabled_fields: [phone]\n"))
		}
		return nil
	}
	outputs := []nativeOutput{}
	for _, step := range r.Case.Steps {
		payload := map[string]any{}
		for key, value := range step.Fields {
			payload[key] = value
		}
		if strings.HasSuffix(step.Operation, ".save") || strings.HasSuffix(step.Operation, ".delete") {
			if _, ok := payload["expected_revision"]; !ok {
				expected := expectedRevision(t, r.Root, step.Operation)
				if step.Stale {
					expected = "stale"
				}
				payload["expected_revision"] = expected
			}
		}
		if payload["answer_id"] == "$first" {
			raw, e := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(AnswersPath)))
			if e != nil {
				t.Fatal(e)
			}
			bank, _, err := answerBank(raw, true)
			if err != nil || len(bank) == 0 {
				t.Fatal("missing fixture answer")
			}
			payload["answer_id"] = bank[0].ID
		}
		out, err := s.Run(ctx, Request{step.Operation, payload})
		if out.Committed && (strings.HasPrefix(step.Operation, "answers.") || step.Operation == "configuration.save") {
			counter++
		} // source's separate staging UUID
		var files map[string]*string
		if r.Mode != "bounds" {
			files = settingsFiles(t, r.Root)
		}
		outputs = append(outputs, nativeOutput{out, err, files})
	}
	if e := json.NewEncoder(os.Stdout).Encode(outputs); e != nil {
		t.Fatal(e)
	}
}

func crashNative(t *testing.T, request nativeRequest) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperNativeProcess$")
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "CAREERHUB_HELPER_NATIVE=1")
	raw, _ := json.Marshal(request)
	cmd.Stdin = bytes.NewReader(raw)
	var output boundedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 86 {
		t.Fatalf("crash not observed: %v %.800s", err, output.Bytes())
	}
}
func native(t *testing.T, request nativeRequest) []nativeOutput {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperNativeProcess$")
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "CAREERHUB_HELPER_NATIVE=1")
	raw, e := json.Marshal(request)
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stdin = bytes.NewReader(raw)
	var output, stderr boundedOutput
	cmd.Stdout, cmd.Stderr = &output, &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("native helper: %v %.1600s %.1000s", e, output.Bytes(), stderr.Bytes())
	}
	var result []nativeOutput
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	if e := decoder.Decode(&result); e != nil {
		t.Fatal(e)
	}
	return result
}
