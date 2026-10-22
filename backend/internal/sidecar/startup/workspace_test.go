package startup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) protocol.StartRecord {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := protocol.StartRecord{ResourceRoot: filepath.Join(base, "resources"), DataRoot: filepath.Join(base, "fresh", "home"), Channel: "dev"}
	writeFixture(t, start.ResourceRoot, "VERSION", "1.2.3\n")
	writeFixture(t, start.ResourceRoot, "src/intake_ui_static", "static presence sentinel")
	writeFixture(t, start.ResourceRoot, catalogstore.CoreGroupsPath, "[]\n")
	writeFixture(t, start.ResourceRoot, catalogstore.CoreSkillsPath, "- {id: core, name: Core, category: tool}\n")
	return start
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	typed, ok := err.(*Error)
	if !ok || typed.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
		if fmt.Sprintf(format, typed) != "Startup workspace could not be prepared." {
			t.Fatal("nonfixed error formatting")
		}
	}
}

func TestPreflightFailureNeverDispatchesBootstrap(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, *protocol.StartRecord)
		code string
	}{
		{"resource", func(t *testing.T, s *protocol.StartRecord) { s.ResourceRoot += "-absent" }, "SIDECAR_RESOURCE_INVALID"},
		{"core", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, catalogstore.CoreSkillsPath, "[broken")
		}, "SIDECAR_SCHEMA_INITIALIZATION_FAILED"},
		{"overlap", func(t *testing.T, s *protocol.StartRecord) { s.DataRoot = filepath.Join(s.ResourceRoot, "uncreated") }, "SIDECAR_PATH_BOUNDARY_INVALID"},
		{"channel", func(t *testing.T, s *protocol.StartRecord) { s.Channel = "nightly" }, "SIDECAR_START_INVALID_CHANNEL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := fixture(t)
			test.edit(t, &start)
			calls := 0
			w, err := open(context.Background(), start, func(context.Context, string, string) (*datahome.Info, *datahome.BootstrapError) {
				calls++
				return nil, nil
			})
			requireCode(t, err, test.code)
			if w != nil || calls != 0 {
				t.Fatal("preflight dispatched bootstrap or returned workspace")
			}
			if _, err := os.Lstat(start.DataRoot); !os.IsNotExist(err) {
				t.Fatal("failed preflight created candidate")
			}
		})
	}
}

func TestCancellationBeforeAndAfterPreparation(t *testing.T) {
	for _, nilContext := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if nilContext {
			ctx = nil
		}
		w, err := open(ctx, protocol.StartRecord{}, func(context.Context, string, string) (*datahome.Info, *datahome.BootstrapError) {
			t.Fatal("canceled startup dispatched bootstrap")
			return nil, nil
		})
		requireCode(t, err, "DATA_HOME_STARTUP_CANCELLED")
		if w != nil {
			t.Fatal("canceled startup returned workspace")
		}
	}
	start := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := open(ctx, start, func(got context.Context, root, channel string) (*datahome.Info, *datahome.BootstrapError) {
		if got != ctx || root != start.DataRoot || channel != start.Channel {
			t.Fatal("bootstrap input changed")
		}
		cancel()
		return nil, nil
	})
	requireCode(t, err, "DATA_HOME_STARTUP_CANCELLED")
	if w != nil {
		t.Fatal("post-bootstrap cancellation admitted workspace")
	}
}

func TestBootstrapErrorDoesNotFallThroughOrExposeDetails(t *testing.T) {
	start := fixture(t)
	w, err := open(context.Background(), start, func(context.Context, string, string) (*datahome.Info, *datahome.BootstrapError) {
		return nil, &datahome.BootstrapError{Code: "DATA_HOME_UNWRITABLE", Stage: "private-stage", CauseCode: "private-cause", MayHaveChanged: true}
	})
	requireCode(t, err, "DATA_HOME_UNWRITABLE")
	if w != nil {
		t.Fatal("bootstrap failure returned workspace")
	}
	for _, code := range []string{"DATA_HOME_UNWRITABLE", "DATA_HOME_RECOVERY_REQUIRED", "ACL_SETUP_FAILED", "ACL_TREE_LIMIT", "ACL_CONTEXT_CANCELED", "DATA_HOME_BOOTSTRAP_UNSUPPORTED", "DATA_HOME_IN_USE", "DATA_HOME_OWNERSHIP_UNAVAILABLE", "DATA_HOME_OWNERSHIP_UNSUPPORTED"} {
		requireCode(t, bootstrapError(&datahome.BootstrapError{Code: code}), code)
	}
	requireCode(t, bootstrapError(&datahome.BootstrapError{Code: "private-value"}), "SIDECAR_STARTUP_FAILED")
	requireCode(t, workspaceError(errors.New("private-value")), "SIDECAR_STARTUP_FAILED")
	requireCode(t, workspaceError(&readonly.Error{Code: "private-value"}), "SIDECAR_STARTUP_FAILED")
}

func TestFinalAdmissionIsRequiredAfterReportedBootstrapSuccess(t *testing.T) {
	start := fixture(t)
	w, err := open(context.Background(), start, func(context.Context, string, string) (*datahome.Info, *datahome.BootstrapError) { return nil, nil })
	requireCode(t, err, "DATA_HOME_UNAVAILABLE")
	if w != nil {
		t.Fatal("bootstrap success bypassed final admission")
	}
}
