//go:build windows

package startup

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/datahome/privacy"
	"golang.org/x/sys/windows"
)

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = info.Mode().String()
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] += fmt.Sprintf(":%d:%x", info.ModTime().UnixNano(), sha256.Sum256(raw))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func dacl(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	text := sd.String()
	if text == "" {
		t.Fatal("native descriptor conversion failed")
	}
	return text
}

func TestNativeOpenBootstrapsMissingAndPrecreatedHomes(t *testing.T) {
	for _, precreated := range []bool{false, true} {
		t.Run(fmt.Sprint(precreated), func(t *testing.T) {
			start := fixture(t)
			if precreated {
				if err := os.MkdirAll(start.DataRoot, 0700); err != nil {
					t.Fatal(err)
				}
			}
			resourcesBefore := snapshot(t, start.ResourceRoot)
			w, err := Open(context.Background(), start)
			if err != nil || w == nil || w.AppVersion() != "1.2.3" {
				t.Fatalf("native startup failed: %v", err)
			}
			if e := privacy.VerifyPrivateTree(context.Background(), start.DataRoot); e != nil {
				t.Fatal(e.Code)
			}
			info, e := datahome.Inspect(start.DataRoot, &start.Channel)
			if e != nil || !info.Initialized {
				t.Fatal("initialized marker missing")
			}
			payload, err := w.Registry()
			if err != nil || payload["skill_count"] != 1 {
				t.Fatalf("registry failed: %v", err)
			}
			if !reflect.DeepEqual(resourcesBefore, snapshot(t, start.ResourceRoot)) {
				t.Fatal("startup changed resources")
			}
			before := snapshot(t, start.DataRoot)
			permissions := dacl(t, start.DataRoot)
			again, err := Open(context.Background(), start)
			if err != nil || again == nil {
				t.Fatalf("restart failed: %v", err)
			}
			afterInfo, e := datahome.Inspect(start.DataRoot, &start.Channel)
			if e != nil || *info != *afterInfo || !reflect.DeepEqual(before, snapshot(t, start.DataRoot)) || permissions != dacl(t, start.DataRoot) {
				t.Fatal("restart changed identity, contents or root ACL")
			}
		})
	}
}

func TestNativeOpenExistingOpaqueAndCustomFilesArePreserved(t *testing.T) {
	start := fixture(t)
	start.Channel = "stable"
	if _, e := datahome.Bootstrap(context.Background(), start.DataRoot, start.Channel); e != nil {
		t.Fatal(e.Code)
	}
	writeFixture(t, start.DataRoot, "career_vault/application_helper.yaml", "opaque: retained\n")
	writeFixture(t, start.DataRoot, "documents/example.bin", "\x00\xffsynthetic\x00")
	writeFixture(t, start.DataRoot, catalogstore.CustomSkillsPath, "- {id: custom, name: Custom, category: tool}\n")
	before := snapshot(t, start.DataRoot)
	permissions := dacl(t, start.DataRoot)
	w, err := Open(context.Background(), start)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := w.Registry()
	if err != nil || payload["skill_count"] != 2 {
		t.Fatal("custom registry unavailable")
	}
	if !reflect.DeepEqual(before, snapshot(t, start.DataRoot)) || permissions != dacl(t, start.DataRoot) {
		t.Fatal("existing private files or permissions changed")
	}
}

func TestNativeOpenAdmissionAndPrivateSchemaRefusalsPreserveState(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, string)
		code string
	}{
		{"recovery", func(t *testing.T, root string) {
			writeFixture(t, root, datahome.DocumentRecoveryPath, "opaque recovery sentinel")
		}, "DATA_HOME_RECOVERY_REQUIRED"},
		{"channel", func(t *testing.T, root string) {
			raw, e := datahome.MarkerBytes("stable", "dh_0123456789abcdef0123456789abcdef")
			if e != nil {
				t.Fatal(e.Code)
			}
			writeFixture(t, root, datahome.MarkerPath, string(raw))
		}, "DATA_HOME_CHANNEL_MISMATCH"},
		{"legacy", func(t *testing.T, root string) {
			writeFixture(t, root, datahome.MarkerPath, `{"application":"CareerHub","format_version":1,"channel":"dev"}`)
		}, "DATA_HOME_MIGRATION_REQUIRED"},
		{"newer", func(t *testing.T, root string) {
			writeFixture(t, root, datahome.MarkerPath, `{"application":"CareerHub","format_version":3,"channel":"dev"}`)
		}, "DATA_HOME_NEWER_THAN_APP"},
		{"invalid_marker", func(t *testing.T, root string) { writeFixture(t, root, datahome.MarkerPath, "invalid") }, "INVALID_DATA_HOME"},
		{"private_schema", func(t *testing.T, root string) {
			writeFixture(t, root, catalogstore.CustomSkillsPath, "[malformed custom")
		}, "SIDECAR_SCHEMA_INITIALIZATION_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := fixture(t)
			if _, e := datahome.Bootstrap(context.Background(), start.DataRoot, start.Channel); e != nil {
				t.Fatal(e.Code)
			}
			test.edit(t, start.DataRoot)
			before := snapshot(t, start.DataRoot)
			permissions := dacl(t, start.DataRoot)
			w, err := Open(context.Background(), start)
			requireCode(t, err, test.code)
			if w != nil || !reflect.DeepEqual(before, snapshot(t, start.DataRoot)) || permissions != dacl(t, start.DataRoot) {
				t.Fatal("refusal mutated initialized home")
			}
		})
	}
}

func TestNativeOpenMarkerlessRecoveryBlocksAllDefaultWrites(t *testing.T) {
	start := fixture(t)
	writeFixture(t, start.DataRoot, datahome.DocumentRecoveryPath, "do not interpret")
	before := snapshot(t, start.DataRoot)
	w, err := Open(context.Background(), start)
	requireCode(t, err, "DATA_HOME_RECOVERY_REQUIRED")
	if w != nil || !reflect.DeepEqual(before, snapshot(t, start.DataRoot)) {
		t.Fatal("markerless recovery created defaults")
	}
}

func TestDefaultReadonlyOpenStillDoesNotBootstrap(t *testing.T) {
	start := fixture(t)
	w, err := readonly.Open(start)
	typed, ok := err.(*readonly.Error)
	if w != nil || !ok || typed.Code != "DATA_HOME_UNAVAILABLE" {
		t.Fatal("default read-only behavior changed")
	}
	if _, err := os.Lstat(start.DataRoot); !os.IsNotExist(err) {
		t.Fatal("read-only open created candidate")
	}
}
