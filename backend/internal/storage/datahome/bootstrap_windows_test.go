//go:build windows

package datahome

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/datahome/privacy"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"golang.org/x/sys/windows"
)

const fixtureHomeID = "dh_0123456789abcdef0123456789abcdef"

func nativeBootstrapOps() bootstrapOperations {
	return bootstrapOperations{
		apply: privacy.ApplyPrivateTree, verify: privacy.VerifyPrivateTree,
		seed: (*runtimepaths.Root).CreateFile, probe: (*runtimepaths.Root).ProbeWritable,
		newID: func() (string, error) { return fixtureHomeID, nil },
	}
}

func bootstrapFixtureWrite(t *testing.T, root, relative string, raw []byte) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func bootstrapDACL(t *testing.T, root string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

// Only called on a test's newly allocated synthetic root, never a user profile.
func broadenBootstrapFixture(t *testing.T, root string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(sd)
}

// The value is either "directory" or the SHA-256 of exact file bytes.
func bootstrapTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == root {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if entry.IsDir() {
			result[key] = "directory"
			return nil
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		result[key] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireBootstrapFailure(t *testing.T, e *BootstrapError, stage, code string, changed bool) {
	t.Helper()
	if e == nil || e.Stage != stage || e.Code != code || e.MayHaveChanged != changed {
		t.Fatalf("unexpected bootstrap failure: %#v; want %s/%s changed=%v", e, stage, code, changed)
	}
}

func TestBootstrapFreshPrivateTreeAndIdempotence(t *testing.T) {
	for _, precreated := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-nested", true: "precreated"}[precreated], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "nested", "home")
			if precreated {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			ops := nativeBootstrapOps()
			seeds := []string{}
			ops.seed = func(r *runtimepaths.Root, name string, raw []byte) (bool, *runtimepaths.Error) {
				if e := privacy.VerifyPrivateTree(context.Background(), root); e != nil {
					t.Fatalf("content reached before private permissions: %s", e.Code)
				}
				seeds = append(seeds, name)
				return r.CreateFile(name, raw)
			}
			info, e := bootstrap(context.Background(), root, "stable", ops)
			if e != nil || info.DataHomeID != fixtureHomeID {
				t.Fatalf("bootstrap failed: %#v", e)
			}
			if len(seeds) != 6 || seeds[len(seeds)-1] != MarkerPath {
				t.Fatal("marker was not the last seeded file")
			}
			before := bootstrapTree(t, root)
			if len(before) != 18 {
				t.Fatalf("unexpected bootstrap structure: %v", before)
			}
			for _, item := range bootstrapDefaults {
				raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.path)))
				if err != nil || string(raw) != item.content {
					t.Fatal("default bytes changed")
				}
			}
			again, e := Bootstrap(context.Background(), root, "stable")
			if e != nil || *again != *info || !reflect.DeepEqual(before, bootstrapTree(t, root)) {
				t.Fatal("repeat bootstrap changed initialized home")
			}
			if e := privacy.VerifyPrivateTree(context.Background(), root); e != nil {
				t.Fatal(e.Code)
			}
		})
	}
}

func TestBootstrapPreservesOpaquePartialAndInitializedContent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	opaque := []byte("not YAML\x00\xff\r\n")
	bootstrapFixtureWrite(t, root, bootstrapDefaults[0].path, opaque)
	bootstrapFixtureWrite(t, root, "career_vault/user-note.bin", opaque)
	broadenBootstrapFixture(t, root)
	info, e := Bootstrap(context.Background(), root, "stable")
	if e != nil {
		t.Fatalf("partial bootstrap failed: %#v", e)
	}
	if e := privacy.VerifyPrivateTree(context.Background(), root); e != nil {
		t.Fatal(e.Code)
	}
	before := bootstrapTree(t, root)
	broadenBootstrapFixture(t, root)
	acl := bootstrapDACL(t, root)
	missing := bootstrapDefaults[1].path
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(missing))); err != nil {
		t.Fatal(err)
	}
	ops := nativeBootstrapOps()
	ops.apply = func(context.Context, string) (bool, *privacy.Error) {
		t.Fatal("initialized ACL reapplied")
		return false, nil
	}
	ops.verify = func(context.Context, string) *privacy.Error {
		t.Fatal("new permission gate imposed on initialized source home")
		return nil
	}
	again, e := bootstrap(context.Background(), root, "stable", ops)
	if e != nil || *again != *info || acl != bootstrapDACL(t, root) || !reflect.DeepEqual(before, bootstrapTree(t, root)) {
		t.Fatalf("existing home changed unexpectedly: %#v", e)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(bootstrapDefaults[0].path)))
	if err != nil || !reflect.DeepEqual(raw, opaque) {
		t.Fatal("opaque user default reset")
	}
}

func TestBootstrapInterruptedSeedsAndProbeAreRetryable(t *testing.T) {
	for _, stopped := range []string{bootstrapDefaults[2].path, MarkerPath, "probe"} {
		t.Run(strings.ReplaceAll(stopped, "/", "-"), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "home")
			ops := nativeBootstrapOps()
			ops.seed = func(r *runtimepaths.Root, name string, raw []byte) (bool, *runtimepaths.Error) {
				if name == stopped {
					return false, &runtimepaths.Error{Code: "SYNTHETIC_WRITE_FAILURE"}
				}
				return r.CreateFile(name, raw)
			}
			if stopped == "probe" {
				ops.probe = func(*runtimepaths.Root) *runtimepaths.Error {
					return &runtimepaths.Error{Code: "SYNTHETIC_PROBE_FAILURE"}
				}
			}
			_, e := bootstrap(context.Background(), root, "stable", ops)
			stage := "defaults"
			if stopped == MarkerPath {
				stage = "marker"
			}
			if stopped == "probe" {
				stage = "probe"
			}
			requireBootstrapFailure(t, e, stage, "DATA_HOME_UNWRITABLE", true)
			_, err := os.Stat(filepath.Join(root, MarkerPath))
			if (err == nil) != (stopped == "probe") {
				t.Fatal("interrupted bootstrap marker boundary wrong")
			}
			ops = nativeBootstrapOps()
			applies := 0
			ops.apply = func(ctx context.Context, root string) (bool, *privacy.Error) {
				applies++
				return privacy.ApplyPrivateTree(ctx, root)
			}
			info, e := bootstrap(context.Background(), root, "stable", ops)
			wantApplies := 1
			if stopped == "probe" {
				wantApplies = 0
			}
			if e != nil || info.DataHomeID != fixtureHomeID || applies != wantApplies {
				t.Fatalf("retry failed or wrong ACL lifecycle: %#v", e)
			}
			if len(bootstrapTree(t, root)) != 18 {
				t.Fatal("retry left incomplete or staging files")
			}
		})
	}
}

func TestBootstrapRefusesUnsafeAdmissionBeforeMutation(t *testing.T) {
	canonical, _ := MarkerBytes("stable", fixtureHomeID)
	cases := []struct {
		name, relative, content, stage, code string
		directory                            bool
	}{
		{"invalid-marker", MarkerPath, "{}", "admission", "INVALID_DATA_HOME", false},
		{"old-marker", MarkerPath, strings.Replace(string(canonical), "\"format_version\":2", "\"format_version\":1", 1), "admission", "DATA_HOME_MIGRATION_REQUIRED", false},
		{"new-marker", MarkerPath, strings.Replace(string(canonical), "\"format_version\":2", "\"format_version\":3", 1), "admission", "DATA_HOME_NEWER_THAN_APP", false},
		{"other-channel", MarkerPath, strings.Replace(string(canonical), "stable", "dev", 1), "admission", "DATA_HOME_CHANNEL_MISMATCH", false},
		{"document-recovery", DocumentRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"restore-recovery", RestoreRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"registry-recovery", RegistryRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"evidence-recovery", EvidenceRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"discovery-recovery", DiscoveryRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"resume-recovery", ResumeRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"intake-recovery", IntakeRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"new-job-recovery", NewJobRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"approval-recovery", ApprovalRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"email-credential-recovery", EmailCredentialRecoveryPath, "opaque", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"unknown-entry", "unrecognized.txt", "opaque", "partial", "PARTIAL_DATA_HOME", false},
		{"orphan-marker-stage", ".career_hub_data_home.json.crashed.tmp", "opaque", "partial", "PARTIAL_DATA_HOME", false},
		{"default-directory", bootstrapDefaults[0].path, "", "default_path", "DATA_HOME_UNWRITABLE", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "home")
			if c.directory {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(c.relative)), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				bootstrapFixtureWrite(t, root, c.relative, []byte(c.content))
			}
			before, acl := bootstrapTree(t, root), bootstrapDACL(t, root)
			_, e := Bootstrap(context.Background(), root, "stable")
			requireBootstrapFailure(t, e, c.stage, c.code, false)
			if !reflect.DeepEqual(before, bootstrapTree(t, root)) || acl != bootstrapDACL(t, root) {
				t.Fatal("refused admission changed fixture")
			}
		})
	}
	for _, sentinel := range []string{DocumentRecoveryPath, RestoreRecoveryPath, RegistryRecoveryPath, EvidenceRecoveryPath, DiscoveryRecoveryPath, ResumeRecoveryPath, IntakeRecoveryPath, NewJobRecoveryPath, EmailCredentialRecoveryPath, ApprovalRecoveryPath} {
		root := filepath.Join(t.TempDir(), "home")
		bootstrapFixtureWrite(t, root, MarkerPath, canonical)
		bootstrapFixtureWrite(t, root, sentinel, []byte("opaque"))
		before := bootstrapTree(t, root)
		_, e := Bootstrap(context.Background(), root, "stable")
		requireBootstrapFailure(t, e, "admission", "DATA_HOME_RECOVERY_REQUIRED", false)
		if !reflect.DeepEqual(before, bootstrapTree(t, root)) {
			t.Fatal("initialized recovery mutated")
		}
	}
}

func TestBootstrapCancellationPermissionsIdentityAndConflict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, input := range []struct {
		ctx                  context.Context
		channel, stage, code string
	}{
		{context.Background(), "bad", "channel", "INVALID_CHANNEL"},
		{nil, "stable", "cancelled", "DATA_HOME_STARTUP_CANCELLED"},
		{ctx, "stable", "cancelled", "DATA_HOME_STARTUP_CANCELLED"},
	} {
		root := filepath.Join(t.TempDir(), "missing")
		_, e := Bootstrap(input.ctx, root, input.channel)
		requireBootstrapFailure(t, e, input.stage, input.code, false)
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid input created root")
		}
	}
	for _, mode := range []string{"permission-before", "permission-after", "verification", "identity", "invalid-identity", "conflict", "cancel-after-permissions"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			ops := nativeBootstrapOps()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stage, code, changed := "permissions", "ACL_SETUP_FAILED", false
			switch mode {
			case "permission-before":
				ops.apply = func(context.Context, string) (bool, *privacy.Error) { return false, &privacy.Error{Code: code} }
			case "permission-after":
				changed = true
				ops.apply = func(ctx context.Context, root string) (bool, *privacy.Error) {
					_, e := privacy.ApplyPrivateTree(ctx, root)
					if e != nil {
						t.Fatal(e.Code)
					}
					return true, &privacy.Error{Code: code}
				}
			case "verification":
				changed = true
				ops.verify = func(context.Context, string) *privacy.Error { return &privacy.Error{Code: code} }
			case "identity":
				changed = true
				stage = "identity"
				code = "DATA_HOME_ID_UNAVAILABLE"
				ops.newID = func() (string, error) { return "", errors.New("synthetic entropy failure") }
			case "invalid-identity":
				changed = true
				stage = "identity"
				code = "INVALID_DATA_HOME"
				ops.newID = func() (string, error) { return "invalid", nil }
			case "conflict":
				changed = true
				stage = "defaults"
				code = "DEFAULT_CONFLICT"
				ops.seed = func(r *runtimepaths.Root, name string, raw []byte) (bool, *runtimepaths.Error) {
					bootstrapFixtureWrite(t, root, name, []byte("intervening creator"))
					return r.CreateFile(name, raw)
				}
			case "cancel-after-permissions":
				changed = true
				stage = "cancelled"
				code = "DATA_HOME_STARTUP_CANCELLED"
				ops.apply = func(ctx context.Context, root string) (bool, *privacy.Error) {
					changed, e := privacy.ApplyPrivateTree(ctx, root)
					cancel()
					return changed, e
				}
			}
			_, e := bootstrap(ctx, root, "stable", ops)
			requireBootstrapFailure(t, e, stage, code, changed)
			if _, err := os.Stat(filepath.Join(root, MarkerPath)); !os.IsNotExist(err) {
				t.Fatal("failed pre-marker bootstrap published marker")
			}
			if mode == "conflict" {
				raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(bootstrapDefaults[0].path)))
				if err != nil || string(raw) != "intervening creator" {
					t.Fatal("late creator overwritten")
				}
			}
		})
	}
}

func TestBootstrapMarkerPublishedBeforeReportedFailureKeepsIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	ops := nativeBootstrapOps()
	ops.seed = func(r *runtimepaths.Root, name string, raw []byte) (bool, *runtimepaths.Error) {
		created, e := r.CreateFile(name, raw)
		if e == nil && name == MarkerPath {
			return created, &runtimepaths.Error{Code: "SYNTHETIC_AFTER_PUBLICATION"}
		}
		return created, e
	}
	_, e := bootstrap(context.Background(), root, "stable", ops)
	requireBootstrapFailure(t, e, "marker", "DATA_HOME_UNWRITABLE", true)
	before := bootstrapTree(t, root)
	ops = nativeBootstrapOps()
	ops.apply = func(context.Context, string) (bool, *privacy.Error) {
		t.Fatal("published marker lost on retry")
		return false, nil
	}
	info, e := bootstrap(context.Background(), root, "stable", ops)
	if e != nil || info.DataHomeID != fixtureHomeID || !reflect.DeepEqual(before, bootstrapTree(t, root)) {
		t.Fatal("post-publication failure reset an initialized identity")
	}
}
