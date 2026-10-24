package datahome

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

const homeID = "dh_0123456789abcdef0123456789abcdef"
const validMarker = `{"application":"CareerHub","channel":"stable","data_home_id":"` + homeID + `","format_version":2}`

func TestMarkerSourceTypesAndOrder(t *testing.T) {
	for _, test := range []struct{ name, raw, code, unsupported string }{
		{"valid", validMarker, "", ""},
		{"whitespace", " \n" + validMarker + "\r\n\t", "", ""},
		{"duplicate-last", strings.Replace(validMarker, `"format_version":2`, `"format_version":1,"format_version":2`, 1), "", ""},
		{"overwritten-nan", strings.Replace(validMarker, `"format_version":2`, `"format_version":NaN,"format_version":2`, 1), "", ""},
		{"overwritten-infinity", strings.Replace(validMarker, `"format_version":2`, `"format_version":-Infinity,"format_version":2`, 1), "", ""},
		{"duplicate-old-last", strings.Replace(validMarker, `"format_version":2`, `"format_version":2,"format_version":1`, 1), "DATA_HOME_MIGRATION_REQUIRED", ""},
		{"extra", strings.Replace(validMarker, "}", `,"extra":null}`, 1), "INVALID_DATA_HOME", ""},
		{"newer-before-extra", `{"application":"CareerHub","channel":"stable","format_version":3,"extra":0}`, "DATA_HOME_NEWER_THAN_APP", ""},
		{"old-before-fields", `{"application":"CareerHub","channel":"stable","format_version":1}`, "DATA_HOME_MIGRATION_REQUIRED", ""},
		{"unhashable-channel", strings.Replace(validMarker, `"stable"`, `[]`, 1), "DATA_HOME_INPUT_UNSUPPORTED", "unhashable_channel"},
		{"bad-app-before-channel", `{"application":[],"channel":[],"format_version":2}`, "INVALID_DATA_HOME", ""},
		{"bad-version-before-channel", `{"application":"CareerHub","channel":{},"format_version":2.0}`, "INVALID_DATA_HOME", ""},
		{"channel-before-newer", `{"application":"CareerHub","channel":null,"format_version":3}`, "INVALID_DATA_HOME", ""},
		{"upper-id", strings.Replace(validMarker, homeID, strings.ToUpper(homeID), 1), "INVALID_DATA_HOME", ""},
		{"id-null", strings.Replace(validMarker, `"`+homeID+`"`, `null`, 1), "INVALID_DATA_HOME", ""},
		{"nul-id", strings.Replace(validMarker, homeID, `dh_\u0000`, 1), "INVALID_DATA_HOME", ""},
		{"bad-utf8", validMarker + "\xff", "INVALID_DATA_HOME", ""},
		{"bom", "\xef\xbb\xbf" + validMarker, "INVALID_DATA_HOME", ""},
		{"trailing", validMarker + "{}", "INVALID_DATA_HOME", ""},
		{"bad-json", `{"bad":`, "INVALID_DATA_HOME", ""},
		{"too-large", strings.Repeat(" ", MaxMarkerBytes) + validMarker, "DATA_HOME_INPUT_UNSUPPORTED", "marker_size"},
		{"deep", strings.Replace(validMarker, `"format_version":2`, `"discarded":`+strings.Repeat("[", 901)+`0`+strings.Repeat("]", 901)+`,"format_version":2`, 1), "DATA_HOME_INPUT_UNSUPPORTED", "json_depth"},
		{"decoder-depth-ceiling", strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001), "DATA_HOME_INPUT_UNSUPPORTED", "json_depth"},
		{"overwritten-decoder-depth-ceiling", strings.Replace(validMarker, `"format_version":2`, `"format_version":`+strings.Repeat("[", 10001)+`0`+strings.Repeat("]", 10001)+`,"format_version":2`, 1), "DATA_HOME_INPUT_UNSUPPORTED", "json_depth"},
		{"incomplete-outside-depth-budget", strings.Repeat("[", 901), "DATA_HOME_INPUT_UNSUPPORTED", "json_depth"},
		{"integer-limit-before-decoder-depth-ceiling", `{"ignored":` + strings.Repeat("9", 4301) + `,"deep":` + strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001) + "}", "DATA_HOME_INPUT_UNSUPPORTED", "json_depth"},
	} {
		t.Run(test.name, func(t *testing.T) { assertMarker(t, test.raw, test.code, test.unsupported) })
	}
	for _, raw := range []string{"null", "true", "2", `"CareerHub"`, "[]", "NaN", "{}"} {
		assertMarker(t, raw, "INVALID_DATA_HOME", "")
	}
	for _, version := range []string{"true", "false", "0", "-0", "-1", "-" + strings.Repeat("9", 200)} {
		assertMarker(t, strings.Replace(validMarker, `"format_version":2`, `"format_version":`+version, 1), "DATA_HOME_MIGRATION_REQUIRED", "")
	}
	for _, version := range []string{"2.0", "1e0", "null", `"2"`, "[]", "NaN", "Infinity", "-Infinity"} {
		assertMarker(t, strings.Replace(validMarker, `"format_version":2`, `"format_version":`+version, 1), "INVALID_DATA_HOME", "")
	}
	for _, version := range []string{"3", strings.Repeat("9", 200), strings.Repeat("9", 4300)} {
		assertMarker(t, strings.Replace(validMarker, `"format_version":2`, `"format_version":`+version, 1), "DATA_HOME_NEWER_THAN_APP", "")
	}
	assertMarker(t, strings.Replace(validMarker, `"format_version":2`, `"format_version":`+strings.Repeat("9", 4301), 1), "DATA_HOME_INPUT_UNSUPPORTED", "integer_digits")
}

func assertMarker(t *testing.T, raw, want, limit string) {
	t.Helper()
	info, e := parseMarker([]byte(raw))
	if want == "" {
		if e != nil || info == nil || *info != (Info{2, "stable", true, homeID}) {
			t.Fatalf("unexpected result %#v, %#v", info, e)
		}
	} else if info != nil || e == nil || e.Code != want || e.Unsupported != limit {
		t.Fatalf("got %#v %#v; want %s / %s", info, e, want, limit)
	}
}

func TestDocumentMigrationMarkerModeIsLegacyOnlyAndByteOnly(t *testing.T) {
	legacy := `{"application":"CareerHub","channel":"stable","format_version":1}`
	for _, raw := range []string{
		legacy,
		" {\"application\":\"CareerHub\",\"channel\":\"stable\",\"format_version\":true} \n",
		`{"format_version":2,"format_version":1,"channel":"stable","application":"CareerHub"}`,
	} {
		info, problem := ParseMarkerForDocumentMigration([]byte(raw))
		if problem != nil || info == nil || *info != (Info{1, "stable", true, ""}) {
			t.Fatalf("legacy marker was not admitted for explicit migration: %#v / %#v", info, problem)
		}
		if ordinary, ordinaryProblem := parseMarker([]byte(raw)); ordinary != nil || ordinaryProblem == nil || ordinaryProblem.Code != "DATA_HOME_MIGRATION_REQUIRED" {
			t.Fatalf("ordinary admission accepted legacy marker: %#v / %#v", ordinary, ordinaryProblem)
		}
	}

	current, problem := ParseMarkerForDocumentMigration([]byte(validMarker))
	if problem != nil || current == nil || *current != (Info{2, "stable", true, homeID}) {
		t.Fatalf("current marker changed under migration parser: %#v / %#v", current, problem)
	}
	for _, test := range []struct {
		raw, code string
	}{
		{strings.Replace(legacy, "}", `,"extra":0}`, 1), "DATA_HOME_MIGRATION_REQUIRED"},
		{`{"application":"CareerHub","channel":"stable","format_version":3}`, "DATA_HOME_NEWER_THAN_APP"},
		{`{"application":"CareerHub","channel":[],"format_version":1}`, "DATA_HOME_INPUT_UNSUPPORTED"},
	} {
		if info, failure := ParseMarkerForDocumentMigration([]byte(test.raw)); info != nil || failure == nil || failure.Code != test.code {
			t.Fatalf("got %#v / %#v, want %s", info, failure, test.code)
		}
	}
}

type fakeReader struct {
	marker      []byte
	present     bool
	failure     *runtimepaths.Error
	sentinel    string
	sentinels   map[string]bool
	existsError *runtimepaths.Error
	calls       []string
}

func (r *fakeReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	r.calls = append(r.calls, "read:"+path)
	if path != MarkerPath || limit != MaxMarkerBytes {
		panic("unexpected metadata read")
	}
	return r.marker, r.present, r.failure
}
func (r *fakeReader) Exists(path string) (bool, *runtimepaths.Error) {
	r.calls = append(r.calls, "exists:"+path)
	return path == r.sentinel || r.sentinels[path], r.existsError
}

func TestRecoveryClassificationIsOwnedMetadataOnly(t *testing.T) {
	t.Parallel()
	ordered := []recoverySentinel(orderedRecoverySentinels[:])
	wantCalls := []string{"read:" + MarkerPath}
	for _, sentinel := range ordered {
		wantCalls = append(wantCalls, "exists:"+sentinel.path)
	}
	for _, sentinel := range ordered {
		t.Run(string(sentinel.kind), func(t *testing.T) {
			t.Parallel()
			r := &fakeReader{marker: []byte(validMarker), present: true, sentinel: sentinel.path}
			got, problem := classifyRecovery(r, nil)
			if problem != nil || got == nil || got.Cardinality != RecoverySingle || got.Kind != sentinel.kind || got.Marker != RecoveryMarkerInitialized {
				t.Fatalf("wrong classification: %#v / %#v", got, problem)
			}
			if !reflect.DeepEqual(r.calls, wantCalls) {
				t.Fatalf("classifier did not inspect the fixed metadata set: %v", r.calls)
			}
		})
	}

	zero := &fakeReader{marker: []byte(validMarker), present: true}
	got, problem := classifyRecovery(zero, nil)
	if problem != nil || got == nil || *got != (RecoveryClassification{Cardinality: RecoveryNone, Marker: RecoveryMarkerInitialized}) {
		t.Fatalf("wrong zero classification: %#v / %#v", got, problem)
	}
	multiple := &fakeReader{sentinels: map[string]bool{DocumentRecoveryPath: true, EmailCredentialRecoveryPath: true}}
	got, problem = classifyRecovery(multiple, nil)
	if problem != nil || got == nil || *got != (RecoveryClassification{Cardinality: RecoveryMultiple, Marker: RecoveryMarkerless}) {
		t.Fatalf("wrong multiple classification: %#v / %#v", got, problem)
	}
	if !reflect.DeepEqual(multiple.calls, wantCalls) {
		t.Fatalf("markerless classifier did not remain bounded: %v", multiple.calls)
	}
	if got, problem = ClassifyOwnedRecovery(nil, nil); got != nil || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unowned classification succeeded: %#v / %#v", got, problem)
	}
}

func TestRecoveryClassificationPreservesAdmissionErrors(t *testing.T) {
	t.Parallel()
	invalid := &fakeReader{marker: []byte("{}"), present: true, sentinel: NewJobRecoveryPath}
	if got, problem := classifyRecovery(invalid, nil); got != nil || problem == nil || problem.Code != "INVALID_DATA_HOME" || len(invalid.calls) != 1 {
		t.Fatalf("invalid marker was downgraded: %#v / %#v / %v", got, problem, invalid.calls)
	}
	wrong := "dev"
	mismatch := &fakeReader{marker: []byte(validMarker), present: true, sentinel: NewJobRecoveryPath}
	if got, problem := classifyRecovery(mismatch, &wrong); got != nil || problem == nil || problem.Code != "DATA_HOME_CHANNEL_MISMATCH" {
		t.Fatalf("channel mismatch was downgraded: %#v / %#v", got, problem)
	}
	failed := &fakeReader{marker: []byte(validMarker), present: true, existsError: &runtimepaths.Error{Code: "PATH_CHANGED"}}
	if got, problem := classifyRecovery(failed, nil); got != nil || problem == nil || problem.Code != "DATA_HOME_UNAVAILABLE" {
		t.Fatalf("existence failure was downgraded: %#v / %#v", got, problem)
	}
}

func TestAdmissionOrderNoJournalReads(t *testing.T) {
	wrong := "dev"
	ordered := []string{DocumentRecoveryPath, RestoreRecoveryPath, RegistryRecoveryPath, EvidenceRecoveryPath, DiscoveryRecoveryPath, ResumeRecoveryPath, IntakeRecoveryPath, NewJobRecoveryPath, EmailCredentialRecoveryPath, ApprovalRecoveryPath}
	for _, sentinel := range append(append([]string{}, ordered...), "") {
		r := &fakeReader{marker: []byte(validMarker), present: true, sentinel: sentinel}
		info, e := inspect(r, &wrong)
		want := "DATA_HOME_RECOVERY_REQUIRED"
		if sentinel == "" {
			want = "DATA_HOME_CHANNEL_MISMATCH"
		}
		if info != nil || e == nil || e.Code != want {
			t.Fatalf("%#v %#v", info, e)
		}
		calls := []string{"read:" + MarkerPath}
		for _, path := range ordered {
			calls = append(calls, "exists:"+path)
			if path == sentinel {
				break
			}
		}
		if !reflect.DeepEqual(r.calls, calls) {
			t.Fatalf("order: %v", r.calls)
		}
	}
	r := &fakeReader{marker: []byte("{}"), present: true, sentinel: DocumentRecoveryPath}
	_, e := inspect(r, &wrong)
	if e == nil || e.Code != "INVALID_DATA_HOME" || len(r.calls) != 1 {
		t.Fatal("invalid marker checked journal/channel")
	}
	r = &fakeReader{marker: []byte(validMarker), present: true}
	info, e := inspect(r, nil)
	if info == nil || e != nil {
		t.Fatalf("%#v %#v", info, e)
	}
	empty := ""
	if _, e := inspect(r, &empty); e == nil || e.Code != "DATA_HOME_CHANNEL_MISMATCH" {
		t.Fatal("empty expected channel treated as absent")
	}
}

func TestReadFailuresAreRedactedAndNotMissing(t *testing.T) {
	for _, test := range []struct{ pathCode, want, limit string }{
		{"FILE_SIZE_LIMIT", "DATA_HOME_INPUT_UNSUPPORTED", "marker_size"},
		{"PATH_NOT_REGULAR", "INVALID_DATA_HOME", ""}, {"FILE_OPEN", "INVALID_DATA_HOME", ""},
		{"FILE_READ", "INVALID_DATA_HOME", ""}, {"FILE_STAT", "INVALID_DATA_HOME", ""},
		{"PATH_CHANGED", "DATA_HOME_UNAVAILABLE", ""},
	} {
		r := &fakeReader{failure: &runtimepaths.Error{Code: test.pathCode}}
		info, e := inspect(r, nil)
		if info != nil || e == nil || e.Code != test.want || e.Unsupported != test.limit || e.Error() != "data home admission failed" {
			t.Fatalf("%#v %#v", info, e)
		}
	}
	if _, e := inspect(&fakeReader{}, nil); e == nil || e.Code != "DATA_HOME_UNAVAILABLE" {
		t.Fatal("missing marker not refused")
	}
	r := &fakeReader{marker: []byte(validMarker), present: true, existsError: &runtimepaths.Error{Code: "PATH_LINK"}}
	if _, e := inspect(r, nil); e == nil || e.Code != "DATA_HOME_UNAVAILABLE" {
		t.Fatal("unsafe sentinel ignored")
	}
}

func TestNativeReadOnlyAdmissionAndDirectorySentinels(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, MarkerPath)
	if e := os.WriteFile(path, []byte(validMarker), 0600); e != nil {
		t.Fatal(e)
	}
	before, _ := os.Stat(path)
	info, failure := Inspect(root, nil)
	if failure != nil || info == nil || info.DataHomeID != homeID {
		t.Fatalf("%#v %#v", info, failure)
	}
	held, pathError := runtimepaths.Open(root)
	if pathError != nil {
		t.Fatal(pathError.Code)
	}
	if recovered, e := InspectOwnedForIntakeRecovery(held, nil); recovered != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("unclaimed reader obtained intake recovery authority")
	}
	if recovered, e := InspectOwnedForNewJobRecovery(held, nil); recovered != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("unclaimed reader obtained new-job recovery authority")
	}
	if e := held.Close(); e != nil {
		t.Fatal(e)
	}
	content, e := os.ReadFile(path)
	if e != nil || string(content) != validMarker {
		t.Fatal("marker changed")
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		t.Fatal("marker metadata changed")
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 1 {
		t.Fatal("admission bootstrapped files")
	}
	for _, sentinel := range []string{DocumentRecoveryPath, RestoreRecoveryPath, IntakeRecoveryPath, NewJobRecoveryPath, EmailCredentialRecoveryPath} {
		fixture, e := filepath.EvalSymlinks(t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(fixture, MarkerPath), []byte(validMarker), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.MkdirAll(filepath.Join(fixture, filepath.FromSlash(sentinel)), 0700); e != nil {
			t.Fatal(e)
		}
		if info, e := Inspect(fixture, nil); info != nil || e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatalf("directory sentinel: %#v %#v", info, e)
		}
	}
}
