package assessmentcontext

import (
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func cacheInput() CacheInputs {
	return CacheInputs{Month: Month{2026, 9}, Versions: EngineVersions{11, 4, 4, 3, 1, 1, 1},
		Capture: CacheCapture{"discovery_synthetic", strings.Repeat("a", 64), "confirmed_full", "Engineer", "Example", "https://example.test/job/1"}}
}

func TestV2PresenceFramingFixesLegacyCollision(t *testing.T) {
	data, resources := &memoryReader{files: map[string][]byte{}}, &memoryReader{files: map[string][]byte{}}
	var old []string
	seen := map[string]bool{}
	for _, raw := range [][]byte{nil, {}, []byte("<missing>"), []byte("\x00"), []byte("x\x00evaluation_month\x002026-09")} {
		if raw == nil {
			delete(data.files, contextFiles[2].path)
		} else {
			data.files[contextFiles[2].path] = raw
		}
		legacy, err := ReadContextRevision(context.Background(), data, resources, cacheInput().Month)
		if err != nil {
			t.Fatal(err)
		}
		old = append(old, legacy)
		got, err := ReadCacheIdentity(context.Background(), data, resources, cacheInput())
		if err != nil || seen[got.Key] {
			t.Fatal("presence or raw bytes did not invalidate v2")
		}
		seen[got.Key] = true
	}
	if old[0] != old[2] {
		t.Fatal("actual legacy collision unexpectedly changed")
	}
	for _, pair := range [][2]string{{"a\x00b", "c"}, {"a", "b\x00c"}} {
		input := cacheInput()
		input.Capture.Company, input.Capture.Title = pair[0], pair[1]
		key, err := ReadCacheIdentity(context.Background(), data, resources, input)
		if err != nil || seen[key.Key] {
			t.Fatal("capture delimiter framing collision")
		}
		seen[key.Key] = true
	}
}

func TestV2RejectsInvalidInputBeforeIO(t *testing.T) {
	reader := &memoryReader{}
	for _, mutate := range []func(*CacheInputs){
		func(i *CacheInputs) { i.Month.Year = 0 },
		func(i *CacheInputs) { i.Versions.Evaluator = 0 },
		func(i *CacheInputs) { i.Capture.Scope = "selection" },
		func(i *CacheInputs) { i.Capture.Scope = "link_only" },
		func(i *CacheInputs) { i.Capture.ContentSHA256 = strings.Repeat("A", 64) },
		func(i *CacheInputs) { i.Capture.DiscoveryID = " " },
		func(i *CacheInputs) { i.Capture.Company = "\xff" },
		func(i *CacheInputs) { i.Capture.ObservedURL = strings.Repeat("x", 16<<10+1) },
	} {
		input := cacheInput()
		mutate(&input)
		if got, err := ReadCacheIdentity(context.Background(), reader, reader, input); err == nil || got != (CacheIdentity{}) {
			t.Fatal("invalid input returned identity")
		}
	}
	if got, err := ReadCacheIdentity(nil, reader, reader, cacheInput()); err == nil || got != (CacheIdentity{}) {
		t.Fatal("nil context accepted")
	}
	if len(reader.reads) != 0 {
		t.Fatal("invalid input reached storage")
	}
}

func TestV2RechecksGroupsAndExcludesUnrelatedData(t *testing.T) {
	for _, resource := range []bool{false, true} {
		for _, mode := range []string{"presence", "bytes", "read", "limit", "cancel"} {
			ctx, cancel := context.WithCancel(context.Background())
			data, resources := &memoryReader{files: map[string][]byte{}}, &memoryReader{files: map[string][]byte{}}
			spec, reader := cacheContextFiles[2], data
			if resource {
				spec, reader = cacheContextFiles[0], resources
			}
			reader.files[spec.path] = []byte("[]")
			seen := 0
			reader.onRead = func(path string, count int) ([]byte, bool, *runtimepaths.Error, bool) {
				if path != spec.path {
					return nil, false, nil, false
				}
				seen++
				if seen != 2 {
					return nil, false, nil, false
				}
				switch mode {
				case "presence":
					return nil, false, nil, true
				case "bytes":
					return []byte("[changed]"), true, nil, true
				case "read":
					return nil, false, &runtimepaths.Error{Code: "PRIVATE_CAUSE"}, true
				case "limit":
					return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}, true
				case "cancel":
					cancel()
				}
				return nil, false, nil, false
			}
			got, err := ReadCacheIdentity(ctx, data, resources, cacheInput())
			cancel()
			if err == nil || got != (CacheIdentity{}) || seen != 2 {
				t.Fatal("group recheck/cancellation returned partial key")
			}
		}
	}
	data, resources := &memoryReader{files: map[string][]byte{}}, &memoryReader{files: map[string][]byte{}}
	before, err := ReadCacheIdentity(context.Background(), data, resources, cacheInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"career_vault/profile.yaml", "career_vault/style_rules.yaml", "career_vault/proof_blocks.yaml", "documents/private.bin", "settings.json"} {
		data.files[path] = []byte("private unrelated source")
	}
	after, err := ReadCacheIdentity(context.Background(), data, resources, cacheInput())
	if err != nil || after != before {
		t.Fatal("unrelated personal data included in identity")
	}
	allowed := map[string]bool{}
	for _, spec := range candidateFiles {
		allowed[spec.path] = true
	}
	for _, spec := range cacheContextFiles {
		allowed[spec.path] = true
	}
	for _, path := range append(data.reads, resources.reads...) {
		if !allowed[path] {
			t.Fatal("unrelated input read")
		}
	}
}

func TestCacheIdentityNeverAuthorizesUnvalidatedReuse(t *testing.T) {
	reader := &memoryReader{files: map[string][]byte{}}
	current, err := ReadCacheIdentity(context.Background(), reader, reader, cacheInput())
	if err != nil {
		t.Fatal(err)
	}
	for stored, want := range map[string]CacheKeyDisposition{
		"": CacheKeyRecompute, "sha256:" + strings.Repeat("a", 64): CacheKeyLegacy,
		"ch-assessment-v3:sha256:" + strings.Repeat("a", 64): CacheKeyInvalid,
		assessmentKeyPrefix + strings.Repeat("b", 64):        CacheKeyRecompute,
		assessmentKeyPrefix + strings.Repeat("A", 64):        CacheKeyInvalid,
		current.Key: CacheKeyNeedsValidation, "invalid": CacheKeyInvalid,
	} {
		if got := CheckCacheKey(stored, current); got != want {
			t.Fatalf("cache disposition %s != %s", got, want)
		}
	}
	current.Version = 1
	if CheckCacheKey(current.Key, current) != CacheKeyInvalid {
		t.Fatal("a legacy current identity can reuse itself")
	}
}
