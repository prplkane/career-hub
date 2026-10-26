package vaultread

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type fixtureReader struct {
	files   map[string][]byte
	reads   []string
	before  func(string)
	failure string
}

func (f *fixtureReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if limit != cataloginput.YAMLMaxInputBytes {
		panic("incorrect fixed read bound")
	}
	f.reads = append(f.reads, path)
	if f.failure != "" {
		return nil, false, &runtimepaths.Error{Code: f.failure}
	}
	if f.before != nil {
		f.before(path)
	}
	raw, found := f.files[path]
	return raw, found, nil
}

func TestNativeReadFailuresStayDistinctFromYAMLFailures(t *testing.T) {
	for _, tc := range []struct{ native, code string }{
		{"FILE_SIZE_LIMIT", "VAULT_INPUT_LIMIT"}, {"FILE_READ", "VAULT_READ_FAILED"}, {"PRIVATE-CAUSE", "VAULT_READ_FAILED"},
	} {
		f := shapeFixture()
		f.failure = tc.native
		state, err := loadVault(context.Background(), f)
		if state != nil || err == nil || err.Stage != "read" || err.Code != tc.code || err.Source != "profile" || err.Input != nil {
			t.Fatal("native cause escaped or changed boundary")
		}
	}
}

func shapeFixture() *fixtureReader {
	f := &fixtureReader{files: map[string][]byte{}}
	for _, spec := range schemas {
		raw := "{}\n"
		if spec.kind == "sequence" {
			raw = "[]\n"
		}
		f.files[spec.path] = []byte(raw)
	}
	return f
}

func TestAllFilesLoadInSourceOrderWithoutNormalizingProfile(t *testing.T) {
	f := shapeFixture()
	raw := []byte("name: '  Synthetic Ω  '\r\nrole_targets: [Engineer, Engineer]\r\ncontact: null\r\ncustom: {flag: false, list: [], value: null}\r\n")
	f.files[schemas[profileIndex].path] = raw
	state, err := loadVault(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	var wanted []string
	for _, spec := range schemas {
		wanted = append(wanted, spec.path)
	}
	if !reflect.DeepEqual(f.reads, wanted) {
		t.Fatal("source loading order changed")
	}
	wantValue, yamlErr := cataloginput.ParseYAML(raw)
	if yamlErr != nil || !reflect.DeepEqual(state.documents[profileIndex], wantValue) {
		t.Fatal("profile was normalized or given defaults")
	}
	wantRevision := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if state.revisions[profileIndex] != wantRevision {
		t.Fatal("original revision bytes changed")
	}
	raw[0] = 'X'
	if state.revisions[profileIndex] != wantRevision {
		t.Fatal("profile revision aliases reader memory")
	}
}

func TestLoadFailureOrderAndClassification(t *testing.T) {
	for _, tc := range []struct {
		name, raw, code string
		missing         bool
	}{
		{"missing", "", "VAULT_FILE_MISSING", true},
		{"empty", "", "VAULT_ROOT_TYPE", false},
		{"null", "null\n", "VAULT_ROOT_TYPE", false},
		{"wrong_shape", "[]\n", "VAULT_ROOT_TYPE", false},
		{"invalid_yaml", "[unclosed", "VAULT_YAML_INVALID", false},
		{"invalid_utf8", "\xff", "VAULT_YAML_INVALID", false},
		{"unsupported", "!!set {a: null}\n", "VAULT_INPUT_UNSUPPORTED", false},
		{"bounded", strings.Repeat(" ", cataloginput.YAMLMaxInputBytes+1), "VAULT_INPUT_LIMIT", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := shapeFixture()
			if tc.missing {
				delete(f.files, schemas[educationIndex].path)
			} else {
				f.files[schemas[educationIndex].path] = []byte(tc.raw)
			}
			// A later failure must never supersede the source-order first one.
			f.files[schemas[styleIndex].path] = []byte("[also-invalid")
			state, err := loadVault(context.Background(), f)
			if state != nil || err == nil || err.Code != tc.code || err.Source != "education" {
				t.Fatalf("incorrect failure: %#v", err)
			}
			if len(f.reads) != 2 {
				t.Fatal("load continued after the first failure")
			}
			for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
				if fmt.Sprintf(format, err) != "Career Vault read could not be completed." {
					t.Fatal("error formatting exposed inputs")
				}
			}
		})
	}
}

func TestCanceledReadDoesNotContinueToAnotherFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := shapeFixture()
	f.before = func(string) { cancel() }
	state, err := loadVault(ctx, f)
	if state != nil || err == nil || err.Code != "VAULT_READ_CANCELED" || len(f.reads) != 1 {
		t.Fatal("canceled load continued")
	}
	for _, canceledContext := range []context.Context{ctx, nil} {
		f = shapeFixture()
		if _, err := loadVault(canceledContext, f); err == nil || len(f.reads) != 0 {
			t.Fatal("canceled input reached storage")
		}
	}
}
