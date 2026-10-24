package assessmentcontext

import (
	"careerhub.local/backend/internal/storage/runtimepaths"
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestSavedIdentityDependencyManifest(t *testing.T) {
	data := &memoryReader{files: map[string][]byte{"career_vault/profile.yaml": []byte("target_location: US\nname: Private\n")}}
	resources := &memoryReader{files: map[string][]byte{}}
	month := Month{2026, 9}
	versions := SavedVersions{5, 2, 1}
	source := strings.Repeat("a", 64)
	read := func() Revisions {
		got, e := ReadSavedRevisions(context.Background(), data, resources, month, versions, source)
		if e != nil || !SavedRevisionsValid(got) {
			t.Fatal(e)
		}
		return got
	}
	base := read()
	data.files["career_vault/profile.yaml"] = []byte("target_location: US\nname: Different contact\n")
	if read() != base {
		t.Fatal("contact detail entered identity")
	}
	data.files["career_vault/profile.yaml"] = []byte("target_location: Canada\nname: Private\n")
	if next := read(); next == base || next.Candidate != base.Candidate {
		t.Fatal("profile location not isolated to context")
	}
	data.files["career_vault/profile.yaml"] = []byte("target_location: US\nname: Private\n")
	for _, spec := range cacheContextFiles {
		target := data
		if spec.resource {
			target = resources
		}
		target.files[spec.path] = []byte("[]")
		if next := read(); next == base || next.Candidate != base.Candidate {
			t.Fatal("omitted consumed context", spec.label)
		}
		delete(target.files, spec.path)
	}
	for _, test := range []struct {
		name   string
		modify func()
	}{
		{"source", func() { source = strings.Repeat("b", 64) }},
		{"month", func() { month.Number = 10 }},
		{"evaluator", func() { versions.Evaluator++ }},
		{"validation", func() { versions.Validation++ }},
		{"workflow", func() { versions.Workflow++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.modify()
			if read() == base {
				t.Fatal("not invalidated")
			}
			source = strings.Repeat("a", 64)
			month = Month{2026, 9}
			versions = SavedVersions{5, 2, 1}
		})
	}
	for _, bad := range []string{"", strings.Repeat("A", 64), "sha256:" + source} {
		if _, e := ReadSavedRevisions(context.Background(), data, resources, month, versions, bad); e == nil {
			t.Fatal("invalid raw source identity")
		}
	}
}
func TestSavedIdentityDriftAndCancellation(t *testing.T) {
	for _, mode := range []string{"drift", "failure", "limit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data, resources := &memoryReader{files: map[string][]byte{}}, &memoryReader{files: map[string][]byte{}}
			data.onRead = func(path string, count int) ([]byte, bool, *runtimepaths.Error, bool) {
				if count != 10 {
					return nil, false, nil, false
				}
				switch mode {
				case "drift":
					return []byte("{}"), true, nil, true
				case "failure":
					return nil, false, &runtimepaths.Error{Code: "private error"}, true
				case "limit":
					return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}, true
				case "cancel":
					cancel()
				}
				return nil, false, nil, false
			}
			got, e := ReadSavedRevisions(ctx, data, resources, Month{2026, 9}, SavedVersions{5, 2, 1}, strings.Repeat("a", 64))
			if e == nil || !reflect.DeepEqual(got, Revisions{}) {
				t.Fatal("partial/stale identity")
			}
		})
	}
}
