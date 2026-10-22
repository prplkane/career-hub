//go:build windows

package vaultwrite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

const fixtureMarker = `{"application":"CareerHub","channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef","format_version":2}`

type nativeRequest struct {
	Root, Mode string
	Steps      []referenceStep
	Paths      map[string]string
}
type nativeOutput struct {
	Outcome      *Outcome
	Error        *Error
	Files        map[string]*string
	Replacements int
}

func fixtureWrite(t *testing.T, root, path string, raw []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if e := os.MkdirAll(filepath.Dir(full), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func fixtureFiles(t *testing.T, root string, paths map[string]string) map[string]*string {
	t.Helper()
	files := map[string]*string{}
	for role, path := range paths {
		raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if os.IsNotExist(e) {
			files[role] = nil
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		text := string(raw)
		files[role] = &text
	}
	return files
}

func TestNativeProfileProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_PROFILE_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request nativeRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		t.Fatal("request")
	}
	root, pathErr := runtimepaths.Open(request.Root)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	defer root.Close()
	if request.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	if request.Mode == "pending" {
		fixtureWrite(t, request.Root, datahome.RegistryRecoveryPath, []byte("{}"))
	}
	if request.Mode == "restore" {
		fixtureWrite(t, request.Root, datahome.RestoreRecoveryPath, []byte("{}"))
	}
	var outputs []nativeOutput
	for _, step := range request.Steps {
		service := New(request.Root)
		ctx, cancel := context.WithCancel(context.Background())
		calls, replacements := 0, 0
		service.read = func(r *runtimepaths.Root, path string, bound int64) ([]byte, bool, *runtimepaths.Error) {
			calls++
			if calls == 9 {
				switch request.Mode {
				case "changed-profile":
					full := filepath.Join(request.Root, filepath.FromSlash(request.Paths["profile"]))
					raw, e := os.ReadFile(full)
					if e != nil {
						t.Fatal(e)
					}
					fixtureWrite(t, request.Root, request.Paths["profile"], append([]byte("# external\n"), raw...))
				case "changed-companion":
					fixtureWrite(t, request.Root, request.Paths["education"], []byte("schools: []\n# external\n"))
				case "changed-home":
					fixtureWrite(t, request.Root, datahome.MarkerPath, []byte(strings.ReplaceAll(fixtureMarker, "0123456789abcdef0123456789abcdef", "1123456789abcdef0123456789abcdef")))
				case "pending-late":
					fixtureWrite(t, request.Root, datahome.RegistryRecoveryPath, []byte("{}"))
				case "cancel-before-publish":
					cancel()
				}
			}
			if request.Mode == "target-read-failure" {
				return nil, false, &runtimepaths.Error{Code: "FILE_READ"}
			}
			return r.ReadFile(path, bound)
		}
		service.replace = func(r *runtimepaths.Root, path string, before, after []byte) (bool, *runtimepaths.Error) {
			replacements++
			switch request.Mode {
			case "replace-failure":
				return false, &runtimepaths.Error{Code: "FILE_SYNC"}
			case "replace-conflict":
				return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
			case "unconfirmed":
				return false, nil
			}
			committed, e := r.ReplaceFile(path, before, after)
			if e != nil || !committed {
				return committed, e
			}
			switch request.Mode {
			case "cancel-after-publish":
				cancel()
			case "late-cleanup":
				return true, &runtimepaths.Error{Code: "FILE_CLEANUP"}
			case "changed-after-publish":
				fixtureWrite(t, request.Root, path, append([]byte("# external after\n"), after...))
			case "failed-readback":
				service.read = func(*runtimepaths.Root, string, int64) ([]byte, bool, *runtimepaths.Error) {
					return nil, false, &runtimepaths.Error{Code: "FILE_READ"}
				}
			case "home-after-publish":
				fixtureWrite(t, request.Root, datahome.RegistryRecoveryPath, []byte("{}"))
			}
			return true, nil
		}
		if request.Mode == "close-failure" {
			service.close = func(r *runtimepaths.Root) error { _ = r.Close(); return errors.New("private synthetic close detail") }
		}
		if request.Mode == "nil-read" {
			service.read = nil
		}
		raw, e := os.ReadFile(filepath.Join(request.Root, filepath.FromSlash(request.Paths[step.Target])))
		expected := revision(raw)
		if e != nil || step.Stale {
			expected = "stale"
		}
		release := func() {}
		if request.Mode == "global-gate" {
			var e error
			release, e = operationgate.Acquire(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			cancel()
		}
		if request.Mode == "local-gate" {
			service.gate <- struct{}{}
			cancel()
		}
		if request.Mode == "canceled" {
			cancel()
		}
		var out *Outcome
		var problem *Error
		if step.Target == "profile" {
			out, problem = service.UpdateProfile(ctx, step.ID, step.Payload, expected)
		} else {
			out, problem = service.UpdateEligibility(ctx, step.ID, step.Payload, expected)
		}
		release()
		cancel()
		outputs = append(outputs, nativeOutput{out, problem, fixtureFiles(t, request.Root, request.Paths), replacements})
	}
	if json.NewEncoder(os.Stdout).Encode(outputs) != nil {
		t.Fatal("output")
	}
	os.Exit(0)
}
