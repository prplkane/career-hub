//go:build windows

package materialwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/materialintegrity"
	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/runtimepaths"
	"go.yaml.in/yaml/v4"
)

type toolOutput struct{ buffer bytes.Buffer }

func (b *toolOutput) Write(p []byte) (int, error) {
	if len(p) > (16<<20)-b.buffer.Len() {
		return 0, fmt.Errorf("fixture output limit")
	}
	return b.buffer.Write(p)
}

type nativeOutput struct {
	Outcome *Outcome
	Error   *Error
	Stores  map[string]any
}
type nativeRequest struct {
	Root, Mode string
	Steps      []materials.Request
	Resources  string
	Reviews    []materialintegrity.ReviewRequest
	Exports    []ExportRequest
	Downloads  []ExportReadRequest
	Legacy     []LegacyExportRequest
}

func stores(t *testing.T, root string) map[string]any {
	out := map[string]any{}
	for _, name := range []string{"materials.yaml", "resume.yaml"} {
		raw, e := os.ReadFile(filepath.Join(root, "outputs/application_materials/plan_job", name))
		if os.IsNotExist(e) {
			out[name] = nil
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		var value any
		if e = yaml.Unmarshal(raw, &value); e != nil {
			out[name] = map[string]any{"invalid_yaml": true}
			continue
		}
		out[name] = value
	}
	return out
}
func TestMaterialProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_MATERIAL_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request nativeRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		t.Fatal("request")
	}
	root, pe := runtimepaths.Open(request.Root)
	if pe != nil {
		t.Fatal(pe.Code)
	}
	defer root.Close()
	if request.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e.Code)
		}
	}
	s := New(request.Root)
	s.now = func() time.Time { return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC) }
	if request.Mode == "later-clock" {
		s.now = func() time.Time { return time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if request.Mode == "canceled" {
		cancel()
	}
	var exportedPath string
	s.checkpoint = func(stage string) *Error {
		if request.Mode == "tamper-export:"+stage {
			if exportedPath == "" {
				t.Fatal("export tamper fixture missing")
			}
			f, e := os.OpenFile(exportedPath, os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.WriteString("changed after artifact publication")
			if e != nil {
				t.Fatal(e)
			}
			if e = f.Close(); e != nil {
				t.Fatal(e)
			}
		}
		if request.Mode == "assert-reservation" && stage == "after_read" {
			checkWriterReservation(t, request.Root, true)
		}
		if request.Mode == "crash:"+stage {
			os.Exit(77)
		}
		if request.Mode == "fail:"+stage {
			return failed(stage, "TEST_FAILURE")
		}
		if request.Mode == "cancel:"+stage {
			cancel()
		}
		if request.Mode == "change-core" && stage == "after_read" || request.Mode == "change-core:"+stage {
			original := s.read
			s.read = func(r *runtimepaths.Root, p string, limit int64) ([]byte, bool, *runtimepaths.Error) {
				raw, present, e := original(r, p, limit)
				if e == nil && p == "src/data/job_technologies.yaml" {
					raw = append(raw, []byte("\n# concurrent resource edit\n")...)
				}
				return raw, present, e
			}
		}
		if request.Mode == "change-profile" && stage == "after_read" {
			path := filepath.Join(request.Root, "career_vault/profile.yaml")
			f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.WriteString("\n# independent edit\n")
			if e != nil {
				t.Fatal(e)
			}
			if e = f.Close(); e != nil {
				t.Fatal(e)
			}
		}
		return nil
	}
	if request.Mode == "create-failure" {
		s.create = func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "FILE_WRITE"}
		}
	}
	if request.Mode == "replace-failure" {
		s.replace = func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "FILE_WRITE"}
		}
	}
	if request.Mode == "late-create-error" {
		original := s.create
		s.create = func(r *runtimepaths.Root, p string, b []byte) (bool, *runtimepaths.Error) {
			committed, e := original(r, p, b)
			if e != nil {
				return committed, e
			}
			return committed, &runtimepaths.Error{Code: "FILE_CLEANUP"}
		}
	}
	if request.Mode == "close-failure" {
		s.closeRoot = func(r *runtimepaths.Root) error { _ = r.Close(); return fmt.Errorf("synthetic close failure") }
	}
	if request.Mode == "rollback-failure" {
		s.rollback = func(tx *sql.Tx) error { _ = tx.Rollback(); return fmt.Errorf("synthetic rollback failure") }
	}
	if request.Mode == "late-replace-error" {
		original := s.replace
		s.replace = func(r *runtimepaths.Root, p string, a, b []byte) (bool, *runtimepaths.Error) {
			committed, e := original(r, p, a, b)
			if e != nil {
				return committed, e
			}
			return committed, &runtimepaths.Error{Code: "FILE_CLEANUP"}
		}
	}
	if strings.HasPrefix(request.Mode, "tamper-export:") || request.Mode == "late-receipt-error" {
		original := s.create
		s.create = func(r *runtimepaths.Root, p string, b []byte) (bool, *runtimepaths.Error) {
			published, e := original(r, p, b)
			if e != nil {
				return published, e
			}
			name := filepath.Base(p)
			if strings.HasPrefix(name, "resume.") || strings.HasPrefix(name, "cover_letter.") {
				exportedPath = filepath.Join(request.Root, filepath.FromSlash(p))
			}
			if request.Mode == "late-receipt-error" && strings.Contains(p, "/export_receipts/") {
				return published, &runtimepaths.Error{Code: "FILE_CLEANUP"}
			}
			return published, nil
		}
	}
	outputs := []nativeOutput{}
	for _, step := range request.Steps {
		out, e := s.Run(ctx, step)
		if request.Mode == "assert-reservation" {
			checkWriterReservation(t, request.Root, false)
		}
		outputs = append(outputs, nativeOutput{out, e, stores(t, request.Root)})
	}
	for _, step := range request.Reviews {
		out, e := s.Review(ctx, request.Resources, step)
		if request.Mode == "assert-reservation" {
			checkWriterReservation(t, request.Root, false)
		}
		outputs = append(outputs, nativeOutput{out, e, stores(t, request.Root)})
	}
	for _, step := range request.Exports {
		out, e := s.Export(ctx, request.Resources, step)
		if request.Mode == "assert-reservation" {
			checkWriterReservation(t, request.Root, false)
		}
		outputs = append(outputs, nativeOutput{out, e, stores(t, request.Root)})
	}
	for _, step := range request.Downloads {
		out, e := s.ReadExport(ctx, request.Resources, step)
		outputs = append(outputs, nativeOutput{out, e, stores(t, request.Root)})
	}
	for _, step := range request.Legacy {
		out, e := s.ReadLegacyExport(ctx, step)
		outputs = append(outputs, nativeOutput{out, e, stores(t, request.Root)})
	}
	if json.NewEncoder(os.Stdout).Encode(outputs) != nil {
		t.Fatal("response")
	}
}
