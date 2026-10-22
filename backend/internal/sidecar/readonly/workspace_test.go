package readonly

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/skills/catalog"
)

const fixtureID = "dh_0123456789abcdef0123456789abcdef"

func writeFixture(t *testing.T, root, relative, value string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func marker(channel, identity string) string {
	return fmt.Sprintf(`{"application":"CareerHub","format_version":2,"channel":%q,"data_home_id":%q}`, channel, identity)
}

func fixture(t *testing.T) (string, protocol.StartRecord) {
	t.Helper()
	return fixtureWithResourceName(t, "resources")
}

func fixtureWithResourceName(t *testing.T, resourceName string) (string, protocol.StartRecord) {
	t.Helper()
	// Only the synthetic fixture's OS-created /var alias on macOS is resolved.
	// Product root selection never calls EvalSymlinks or discovers any home.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := protocol.StartRecord{ResourceRoot: filepath.Join(base, resourceName), DataRoot: filepath.Join(base, "data"), Channel: "dev"}
	writeFixture(t, start.ResourceRoot, "VERSION", "1.2.3-dev.01+build.7\n")
	writeFixture(t, start.ResourceRoot, catalogstore.CoreGroupsPath, "[]\n")
	writeFixture(t, start.ResourceRoot, catalogstore.CoreSkillsPath, "- {id: core, name: Core, category: tool}\n")
	if err := os.MkdirAll(filepath.Join(start.ResourceRoot, "src", "intake_ui_static"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, start.DataRoot, datahome.MarkerPath, marker(start.Channel, fixtureID))
	return base, start
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Windows directory-enumeration timestamps can lag earlier fixture child
		// creation until a handle query refreshes them. Compare directory type/mode
		// and membership, and regular-file mtimes/bytes, not cached directory times.
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(":%v:", info.ModTime().UnixNano()) + string(raw)
		}
		result[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	typed, ok := err.(*Error)
	if !ok || typed.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
		if got := fmt.Sprintf(format, typed); got != "Read-only workspace validation failed." {
			t.Fatal("error formatting exposed nonfixed content")
		}
	}
}

func TestInitializedWorkspaceReturnsFreshPayloadWithoutWrites(t *testing.T) {
	for _, channel := range []string{"dev", "stable"} {
		t.Run(channel, func(t *testing.T) {
			base, start := fixture(t)
			start.Channel = channel
			writeFixture(t, start.DataRoot, datahome.MarkerPath, marker(channel, fixtureID))
			writeFixture(t, start.DataRoot, catalogstore.CustomSkillsPath, "- {id: custom, name: Custom, category: tool}\n")
			before := tree(t, base)
			w, err := Open(start)
			if err != nil || w.AppVersion() != "1.2.3-dev.01+build.7" || w.dataHomeID != fixtureID {
				t.Fatalf("initialized open failed: %v", err)
			}
			got, err := w.Registry()
			if err != nil || got["skill_count"] != 2 {
				t.Fatalf("registry payload failed: %v", err)
			}
			expected, loadErr := catalogstore.Load(start.ResourceRoot, start.DataRoot)
			if loadErr != nil || !reflect.DeepEqual(got, expected.Payload()) {
				t.Fatal("catalog payload contract changed")
			}
			got["groups"].([]any)[0].(map[string]any)["name"] = "caller mutation"
			fresh, err := w.Registry()
			if err != nil || !reflect.DeepEqual(fresh, expected.Payload()) {
				t.Fatal("caller payload mutation retained")
			}
			if !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("read-only operation changed fixture tree")
			}
			writeFixture(t, start.DataRoot, catalogstore.CustomSkillsPath, "- {id: custom, name: Changed, category: tool}\n- {id: another, name: Another, category: tool}\n")
			changed, err := w.Registry()
			if err != nil || changed["skill_count"] != 3 || changed["revision"] == fresh["revision"] {
				t.Fatal("later registry read served stale custom catalogs")
			}
		})
	}
}

func TestStartupHomeIsAnImmutableAdmissionCopy(t *testing.T) {
	_, start := fixture(t)
	w, err := Open(start)
	if err != nil {
		t.Fatal(err)
	}
	first, err := w.StartupHome()
	if err != nil || first.DataHomeID != fixtureID || first.Channel != "dev" || first.FormatVersion != 2 || !first.Initialized {
		t.Fatalf("startup marker was not captured: %+v %v", first, err)
	}
	first.DataHomeID = "dh_00000000000000000000000000000000"
	second, err := w.StartupHome()
	if err != nil || second.DataHomeID != fixtureID {
		t.Fatalf("caller changed the stored marker: %+v %v", second, err)
	}
	writeFixture(t, start.DataRoot, datahome.MarkerPath, marker("dev", "dh_11111111111111111111111111111111"))
	if err := w.CheckAdmission(); err == nil {
		t.Fatal("changed marker was promoted into the startup baseline")
	}
	third, err := w.StartupHome()
	if err != nil || third.DataHomeID != fixtureID {
		t.Fatalf("later marker replaced startup identity: %+v %v", third, err)
	}
	var empty *Workspace
	if _, err := empty.StartupHome(); err == nil {
		t.Fatal("nil workspace returned a Home identity")
	}
}

func TestVersionMatchesSourceASCIIAndUniversalNewlines(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		for _, version := range []string{"0.0.0", "12.3.400", "1.2.3-01.alpha--+build.004"} {
			if got, err := parseVersion([]byte(version + newline)); err != nil || got != version {
				t.Fatal("valid source version rejected")
			}
		}
	}
	for _, value := range []string{"", "1.2.3", "1.2.3\n\n", "1.2.3\r\r\n", "01.2.3\n", "v1.2.3\n", "1.2\n", "1.2.3 dev\n", "1.2.3-\n", "1.2.3+\n", "1.2.3-α\n", "\xff\n", "1.2.3\x00\n", strings.Repeat("1", maxVersionBytes) + ".2.3\n"} {
		got, err := parseVersion([]byte(value))
		requireCode(t, err, "SIDECAR_RESOURCE_INVALID")
		if got != "" {
			t.Fatal("invalid version returned bytes")
		}
	}
}

func TestStartFieldsFailBeforeFilesystemAccess(t *testing.T) {
	_, start := fixture(t)
	for _, test := range []struct {
		name string
		edit func(*protocol.StartRecord)
		code string
	}{
		{"port", func(s *protocol.StartRecord) { s.Port = 1 }, "SIDECAR_START_INVALID_PORT"},
		{"channel", func(s *protocol.StartRecord) { s.Channel = "DEV" }, "SIDECAR_START_INVALID_CHANNEL"},
		{"blank_channel", func(s *protocol.StartRecord) { s.Channel = "" }, "SIDECAR_START_INVALID_CHANNEL"},
		{"resource_relative", func(s *protocol.StartRecord) { s.ResourceRoot = "resources" }, "SIDECAR_START_INVALID_PATH"},
		{"data_relative", func(s *protocol.StartRecord) { s.DataRoot = "data" }, "SIDECAR_START_INVALID_PATH"},
		{"empty_path", func(s *protocol.StartRecord) { s.DataRoot = "" }, "SIDECAR_START_INVALID_PATH"},
		{"nul_path", func(s *protocol.StartRecord) { s.DataRoot += "\x00" }, "SIDECAR_START_INVALID_PATH"},
		{"non_utf8_path", func(s *protocol.StartRecord) { s.ResourceRoot += "\xff" }, "SIDECAR_START_INVALID_PATH"},
		{"long_path", func(s *protocol.StartRecord) { s.ResourceRoot += strings.Repeat("x", 4097) }, "SIDECAR_START_INVALID_PATH"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := start
			test.edit(&value)
			w, err := Open(value)
			requireCode(t, err, test.code)
			if w != nil {
				t.Fatal("invalid start admitted")
			}
		})
	}
}

func TestOverlapsAndNativeIdentityAliases(t *testing.T) {
	base, start := fixture(t)
	for _, pair := range [][2]string{{start.ResourceRoot, start.ResourceRoot}, {base, start.DataRoot}, {start.ResourceRoot, base}} {
		value := start
		value.ResourceRoot, value.DataRoot = pair[0], pair[1]
		w, err := Open(value)
		requireCode(t, err, "SIDECAR_PATH_BOUNDARY_INVALID")
		if w != nil {
			t.Fatal("overlapping roots admitted")
		}
	}
	if lexicalOverlap(filepath.Join(base, "data"), filepath.Join(base, "data-backup")) {
		t.Fatal("sibling-prefix false overlap")
	}
	if runtime.GOOS == "windows" && !lexicalOverlap(strings.ToUpper(start.ResourceRoot), start.ResourceRoot) {
		t.Fatal("Windows case alias missed")
	}
	for _, pair := range [][2]string{{base, start.DataRoot}, {start.DataRoot, base}, {start.ResourceRoot, start.ResourceRoot}} {
		if overlap, err := identityOverlap(pair[0], pair[1]); err != nil || !overlap {
			t.Fatal("native identity containment missed")
		}
	}
	if overlap, err := identityOverlap(start.ResourceRoot, start.DataRoot); err != nil || overlap {
		t.Fatal("separate physical roots rejected")
	}
	if _, err := identityOverlap(filepath.Join(base, "missing"), start.DataRoot); err == nil {
		t.Fatal("missing identity accepted")
	}
}

func TestInvalidResourcesAndHomesNeverBootstrapOrFallBack(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, *protocol.StartRecord)
		code string
	}{
		{"missing_resources", func(t *testing.T, s *protocol.StartRecord) { s.ResourceRoot += "-missing" }, "SIDECAR_RESOURCE_INVALID"},
		{"missing_data", func(t *testing.T, s *protocol.StartRecord) { s.DataRoot += "-missing" }, "DATA_HOME_UNAVAILABLE"},
		{"missing_version", func(t *testing.T, s *protocol.StartRecord) { removeFixture(t, s.ResourceRoot, "VERSION") }, "SIDECAR_RESOURCE_INVALID"},
		{"invalid_version", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, "VERSION", "private-invalid-label\n")
		}, "SIDECAR_RESOURCE_INVALID"},
		{"large_version", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, "VERSION", strings.Repeat("1", maxVersionBytes+1))
		}, "SIDECAR_RESOURCE_INVALID"},
		{"missing_static", func(t *testing.T, s *protocol.StartRecord) { removeFixture(t, s.ResourceRoot, "src/intake_ui_static") }, "SIDECAR_RESOURCE_INVALID"},
		{"missing_groups", func(t *testing.T, s *protocol.StartRecord) {
			removeFixture(t, s.ResourceRoot, catalogstore.CoreGroupsPath)
		}, "SIDECAR_RESOURCE_INVALID"},
		{"missing_skills", func(t *testing.T, s *protocol.StartRecord) {
			removeFixture(t, s.ResourceRoot, catalogstore.CoreSkillsPath)
		}, "SIDECAR_RESOURCE_INVALID"},
		{"catalog_directory", func(t *testing.T, s *protocol.StartRecord) {
			removeFixture(t, s.ResourceRoot, catalogstore.CoreSkillsPath)
			if err := os.Mkdir(filepath.Join(s.ResourceRoot, filepath.FromSlash(catalogstore.CoreSkillsPath)), 0700); err != nil {
				t.Fatal(err)
			}
		}, "SIDECAR_RESOURCE_INVALID"},
		{"malformed_core", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, catalogstore.CoreSkillsPath, "- {name: private-invalid-label}\n")
		}, "SIDECAR_SCHEMA_INITIALIZATION_FAILED"},
		{"malformed_custom", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.DataRoot, catalogstore.CustomSkillsPath, "[private-invalid-label")
		}, "SIDECAR_SCHEMA_INITIALIZATION_FAILED"},
		{"missing_marker", func(t *testing.T, s *protocol.StartRecord) { removeFixture(t, s.DataRoot, datahome.MarkerPath) }, "DATA_HOME_UNAVAILABLE"},
		{"invalid_marker", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.DataRoot, datahome.MarkerPath, "private-invalid-label")
		}, "INVALID_DATA_HOME"},
		{"old_marker", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.DataRoot, datahome.MarkerPath, `{"application":"CareerHub","format_version":1,"channel":"dev"}`)
		}, "DATA_HOME_MIGRATION_REQUIRED"},
		{"new_marker", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.DataRoot, datahome.MarkerPath, `{"application":"CareerHub","format_version":3,"channel":"dev"}`)
		}, "DATA_HOME_NEWER_THAN_APP"},
		{"wrong_channel", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.DataRoot, datahome.MarkerPath, marker("stable", fixtureID))
		}, "DATA_HOME_CHANNEL_MISMATCH"},
		{"document_recovery", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.DataRoot, datahome.DocumentRecoveryPath, "do not read this journal")
		}, "DATA_HOME_RECOVERY_REQUIRED"},
		{"restore_recovery", func(t *testing.T, s *protocol.StartRecord) {
			if err := os.Mkdir(filepath.Join(s.DataRoot, datahome.RestoreRecoveryPath), 0700); err != nil {
				t.Fatal(err)
			}
		}, "DATA_HOME_RECOVERY_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, start := fixture(t)
			test.edit(t, &start)
			before := tree(t, base)
			w, err := Open(start)
			requireCode(t, err, test.code)
			if w != nil || !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("invalid admission returned workspace or changed files")
			}
		})
	}
}

func removeFixture(t *testing.T, root, relative string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
		t.Fatal(err)
	}
}

func TestLaterIdentityChannelAndRecoveryChangesBlockReads(t *testing.T) {
	for _, test := range []struct{ name, path, value, code string }{
		{"identity", datahome.MarkerPath, marker("dev", "dh_ffffffffffffffffffffffffffffffff"), "DATA_HOME_IDENTITY_CHANGED"},
		{"channel", datahome.MarkerPath, marker("stable", fixtureID), "DATA_HOME_CHANNEL_MISMATCH"},
		{"invalid", datahome.MarkerPath, "invalid", "INVALID_DATA_HOME"},
		{"document", datahome.DocumentRecoveryPath, "synthetic", "DATA_HOME_RECOVERY_REQUIRED"},
		{"restore", datahome.RestoreRecoveryPath, "synthetic", "DATA_HOME_RECOVERY_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, start := fixture(t)
			w, err := Open(start)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, start.DataRoot, test.path, test.value)
			before := tree(t, base)
			got, err := w.Registry()
			requireCode(t, err, test.code)
			if got != nil || w.dataHomeID != fixtureID || !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("drift read returned data, changed admission identity, or wrote")
			}
		})
	}
}

func TestLaterResourcesAreRevalidatedAndVersionRemainsInitiallyAnnounced(t *testing.T) {
	_, start := fixture(t)
	w, err := Open(start)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, start.ResourceRoot, "VERSION", "9.8.7\n")
	if got, err := w.Registry(); err != nil || got == nil || w.AppVersion() != "1.2.3-dev.01+build.7" {
		t.Fatal("source-style initially announced version changed")
	}
	removeFixture(t, start.ResourceRoot, catalogstore.CoreSkillsPath)
	got, err := w.Registry()
	requireCode(t, err, "SIDECAR_RESOURCE_INVALID")
	if got != nil {
		t.Fatal("missing current resources served a cached registry")
	}
}

func TestConfiguredRootLinksAreRejected(t *testing.T) {
	base, start := fixture(t)
	for _, resource := range []bool{true, false} {
		value := start
		target, link := start.DataRoot, filepath.Join(base, "data-link")
		code := "DATA_HOME_UNAVAILABLE"
		if resource {
			target, link = start.ResourceRoot, filepath.Join(base, "resource-link")
			code = "SIDECAR_RESOURCE_INVALID"
		}
		if err := os.Symlink(target, link); err != nil {
			t.Skip("native synthetic symlink creation unavailable")
		}
		if resource {
			value.ResourceRoot = link
		} else {
			value.DataRoot = link
		}
		w, err := Open(value)
		requireCode(t, err, code)
		if w != nil {
			t.Fatal("configured root link admitted")
		}
	}
}

func TestPostReadDriftAndFailuresDiscardCatalog(t *testing.T) {
	snapshot, err := catalog.Build(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	valid := datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: fixtureID}
	for _, test := range []struct {
		name  string
		after *datahome.Info
		err   *datahome.Error
		code  string
	}{
		{"identity", &datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "different"}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"channel", &datahome.Info{FormatVersion: 2, Channel: "stable", Initialized: true, DataHomeID: fixtureID}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"format", &datahome.Info{FormatVersion: 3, Channel: "dev", Initialized: true, DataHomeID: fixtureID}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"uninitialized", &datahome.Info{}, nil, "DATA_HOME_UNAVAILABLE"},
		{"nil", nil, nil, "DATA_HOME_UNAVAILABLE"},
		{"recovery", nil, &datahome.Error{Code: "DATA_HOME_RECOVERY_REQUIRED"}, "DATA_HOME_RECOVERY_REQUIRED"},
		{"unknown_error", nil, &datahome.Error{Code: "private-invalid-label"}, "DATA_HOME_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reads, loads := 0, 0
			got, id, err := admittedRegistry(fixtureID, func() (*datahome.Info, *datahome.Error) {
				reads++
				if reads == 1 {
					copy := valid
					return &copy, nil
				}
				return test.after, test.err
			}, func() (*catalog.Snapshot, *catalogstore.Error) { loads++; return snapshot, nil })
			requireCode(t, err, test.code)
			if got != nil || id != "" || reads != 2 || loads != 1 {
				t.Fatal("post-read rejection lost")
			}
		})
	}
	for _, before := range []*datahome.Info{nil, {}} {
		got, _, err := admittedRegistry(fixtureID, func() (*datahome.Info, *datahome.Error) { return before, nil }, func() (*catalog.Snapshot, *catalogstore.Error) {
			t.Fatal("invalid admission loaded catalogs")
			return nil, nil
		})
		requireCode(t, err, "DATA_HOME_UNAVAILABLE")
		if got != nil {
			t.Fatal("invalid admission returned data")
		}
	}
	for _, loadErr := range []*catalogstore.Error{nil, {Stage: "input", Code: "private-invalid-label"}} {
		got, _, err := admittedRegistry(fixtureID, func() (*datahome.Info, *datahome.Error) { return &valid, nil }, func() (*catalog.Snapshot, *catalogstore.Error) { return nil, loadErr })
		requireCode(t, err, "SIDECAR_SCHEMA_INITIALIZATION_FAILED")
		if got != nil {
			t.Fatal("failed load returned data")
		}
	}
}

func TestUnavailableWorkspaceAndErrorAllowlist(t *testing.T) {
	for _, w := range []*Workspace{nil, {}} {
		if w.AppVersion() != "" {
			t.Fatal("uninitialized workspace version")
		}
		got, err := w.Registry()
		requireCode(t, err, "DATA_HOME_UNAVAILABLE")
		if got != nil {
			t.Fatal("uninitialized workspace returned data")
		}
	}
	for _, code := range []string{"DATA_HOME_UNAVAILABLE", "INVALID_DATA_HOME", "DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED", "DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_INPUT_UNSUPPORTED"} {
		requireCode(t, admissionError(&datahome.Error{Code: code, Unsupported: "private-invalid-label"}), code)
	}
	requireCode(t, admissionError(&datahome.Error{Code: "private-invalid-label"}), "DATA_HOME_UNAVAILABLE")
	var nilError *Error
	if fmt.Sprintf("%#v", nilError) != "Read-only workspace validation failed." || fmt.Sprintf("%#v", &Error{Code: "private-invalid-label"}) != "Read-only workspace validation failed." {
		t.Fatal("error formatting leaked fields")
	}
}
