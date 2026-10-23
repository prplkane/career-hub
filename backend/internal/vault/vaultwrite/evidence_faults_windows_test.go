//go:build windows

package vaultwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/catalogtransaction"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func evidenceAnnotate(t *testing.T, root, path string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, root, path, append([]byte("# external synthetic edit\n"), raw...))
}

// This fault publisher stages the exact owning-service plan with native writes,
// then interrupts after the record. The engine's individual syscall/crash stages
// are separately qualified in catalogtransaction; this checks service recovery
// of that boundary without exposing a production fault-injection API.
func stageInterruptedEvidence(t *testing.T, r *runtimepaths.Root, home string,
	changes []catalogtransaction.Change, guards []catalogtransaction.Guard) catalogtransaction.Report {
	t.Helper()
	payload, err := json.Marshal(struct {
		Version int                         `json:"version"`
		Home    string                      `json:"data_home_id"`
		Changes []catalogtransaction.Change `json:"changes"`
		Guards  []catalogtransaction.Guard  `json:"guards,omitempty"`
	}{2, home, changes, guards})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	raw, err := json.Marshal(struct {
		Payload json.RawMessage `json:"payload"`
		SHA256  string          `json:"sha256"`
	}{payload, hex.EncodeToString(hash[:])})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := r.CreateFile(datahome.EvidenceRecoveryPath, raw); err != nil || !ok {
		t.Fatal("stage intent", err)
	}
	first := changes[0]
	if ok, err := r.ReplaceFile(first.Path, first.Before, first.After); err != nil || !ok {
		t.Fatal("stage record", err)
	}
	return catalogtransaction.Report{IntentRecorded: true, AppliedFiles: []string{first.Path}}
}

func configureEvidenceFaults(t *testing.T, request evidenceNativeRequest, step evidenceStep,
	s *EvidenceService, cancel context.CancelFunc) {
	t.Helper()
	if step.Op == "recover" {
		return
	}
	target := request.Paths["experiences"]
	if step.Target == "project" {
		target = request.Paths["projects"]
	}
	mode := request.Mode
	read, replace, begin := s.files.read, s.files.replace, s.begin
	loaded, published := false, false
	s.files.read = func(r *runtimepaths.Root, path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
		if mode == "read-error" || mode == "readback-error" && published {
			return nil, false, &runtimepaths.Error{Code: "FILE_READ"}
		}
		raw, present, err := read(r, path, limit)
		if path == request.Paths["style"] && !loaded {
			loaded = true
			switch mode {
			case "changed-target":
				evidenceAnnotate(t, request.Data, target)
			case "changed-companion":
				evidenceAnnotate(t, request.Data, request.Paths["education"])
			case "changed-home":
				fixtureWrite(t, request.Data, datahome.MarkerPath, []byte(strings.ReplaceAll(fixtureMarker, "0123456789abcdef0123456789abcdef", "1123456789abcdef0123456789abcdef")))
			case "cancel-before-publish":
				cancel()
			}
		}
		return raw, present, err
	}
	s.files.replace = func(r *runtimepaths.Root, path string, before, after []byte) (bool, *runtimepaths.Error) {
		switch mode {
		case "false-replace":
			return false, nil
		case "write-error":
			return false, &runtimepaths.Error{Code: "FILE_SYNC"}
		case "replace-conflict":
			return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
		}
		ok, err := replace(r, path, before, after)
		published = ok
		if err == nil && ok && mode == "postwrite-error" {
			return true, &runtimepaths.Error{Code: "FILE_CLEANUP"}
		}
		if err == nil && ok && mode == "cancel-after-publish" {
			cancel()
		}
		return ok, err
	}
	s.begin = func(ctx context.Context, r *runtimepaths.Root, home string, changes []catalogtransaction.Change,
		guards []catalogtransaction.Guard, validate catalogtransaction.Validate) (catalogtransaction.Report, error) {
		switch mode {
		case "late-core-change":
			evidenceAnnotate(t, request.Resources, catalogstore.CoreSkillsPath)
		case "late-home-change":
			fixtureWrite(t, request.Data, datahome.MarkerPath, []byte(strings.ReplaceAll(fixtureMarker, "0123456789abcdef0123456789abcdef", "1123456789abcdef0123456789abcdef")))
		}
		if strings.HasPrefix(mode, "partial") || mode == "crash-after-record" {
			if err := validate(ctx, changes); err != nil {
				return catalogtransaction.Report{}, err
			}
			if strings.HasPrefix(mode, "partial-same") && !bytes.Equal(changes[0].Before, changes[0].After) {
				t.Fatal("same-byte fixture changed the canonical bytes")
			}
			report := stageInterruptedEvidence(t, r, home, changes, guards)
			switch mode {
			case "partial-core-change", "partial-same-core-change":
				fixtureWrite(t, request.Resources, catalogstore.CoreSkillsPath, []byte("[]\n"))
			case "partial-companion-change":
				evidenceAnnotate(t, request.Data, request.Paths["education"])
			case "partial-record-change":
				evidenceAnnotate(t, request.Data, target)
			case "partial-cancel":
				cancel()
				return report, ctx.Err()
			case "crash-after-record":
				os.Exit(86)
			}
			return report, catalogtransaction.ErrIO
		}
		report, err := begin(ctx, r, home, changes, guards, validate)
		if report.Completed {
			published = true
			switch mode {
			case "post-core-change":
				evidenceAnnotate(t, request.Resources, catalogstore.CoreSkillsPath)
			case "post-companion-change":
				evidenceAnnotate(t, request.Data, request.Paths["education"])
			}
		}
		return report, err
	}
	if mode == "cancel-before" {
		cancel()
	}
	if mode == "close-error" {
		s.files.close = func(r *runtimepaths.Root) error { _ = r.Close(); return errors.New("private synthetic close cause") }
	}
}

func evidenceResultRecord(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var result struct{ Record map[string]any }
	if json.Unmarshal(raw, &result) != nil || result.Record["id"] == nil {
		t.Fatal("missing result record")
	}
	return result.Record
}
