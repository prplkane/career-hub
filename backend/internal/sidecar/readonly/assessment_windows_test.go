//go:build windows

package readonly

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
)

type assessmentSourceCase struct {
	Name        string
	Files       map[string]*[]byte
	Year, Month int
	Expected    map[string]struct {
		Value json.RawMessage
		Error string
	}
	Unsupported bool
}
type assessmentRequest struct {
	Start      protocol.StartRecord
	Mode       string
	Cases      []assessmentSourceCase
	CacheCases []assessmentCacheCase
}
type assessmentCacheCase struct {
	Name        string
	Files       map[string]*[]byte
	Input       assessmentcontext.CacheInputs
	Expected    assessmentcontext.CacheIdentity
	Error       string
	Unsupported bool
}

func installCacheFixture(t *testing.T, start protocol.StartRecord, files map[string]*[]byte) {
	t.Helper()
	remaining := make(map[string]*[]byte)
	for key, raw := range files {
		if key != "resource:src/data/skill_groups.yaml" && key != "data:career_vault/taxonomy/skill_groups.yaml" {
			remaining[key] = raw
		}
	}
	installAssessmentFixture(t, start, remaining)
	for _, spec := range []struct{ key, root, relative string }{
		{"resource:src/data/skill_groups.yaml", start.ResourceRoot, "src/data/skill_groups.yaml"},
		{"data:career_vault/taxonomy/skill_groups.yaml", start.DataRoot, "career_vault/taxonomy/skill_groups.yaml"},
	} {
		path := filepath.Join(spec.root, filepath.FromSlash(spec.relative))
		if raw := files[spec.key]; raw != nil {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, *raw, 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal("fixed synthetic group removal failed")
		}
	}
}

func runNativeAssessment(t *testing.T, request assessmentRequest) {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAssessmentProcessHelper$")
	cmd.Env = append(os.Environ(), "CAREERHUB_ASSESSMENT_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if err != nil || cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		t.Fatalf("native synthetic assessment %s failed (deadline=%t, exit=%d): %.3500s", request.Mode, ctx.Err() != nil, exitCode, out)
	}
}

func TestNativeAssessmentAdmissionAndNoWrites(t *testing.T) {
	for _, mode := range []string{"unowned", "identity", "recovery", "resources", "canceled", "directory", "refresh"} {
		t.Run(mode, func(t *testing.T) {
			_, start := fixture(t)
			runNativeAssessment(t, assessmentRequest{Start: start, Mode: mode})
		})
	}
}

var assessmentFixturePaths = map[string]bool{
	"data:career_vault/education.yaml": true, "data:career_vault/visa_profile.yaml": true,
	"data:career_vault/experience_blocks.yaml": true, "data:career_vault/project_blocks.yaml": true,
	"data:career_vault/skill_blocks.yaml": true, "resource:src/data/job_technologies.yaml": true,
	"data:career_vault/taxonomy/technologies.yaml": true, "data:career_vault/hard_blocker_policy.yaml": true,
	"resource:src/data/requirement_concepts.yaml": true, "data:career_vault/profile.yaml": true, "data:documents/private.bin": true,
}

func installAssessmentFixture(t *testing.T, start protocol.StartRecord, files map[string]*[]byte) {
	t.Helper()
	for key := range files {
		if !assessmentFixturePaths[key] {
			t.Fatal("unknown synthetic path")
		}
	}
	for key := range assessmentFixturePaths {
		kind, relative, _ := strings.Cut(key, ":")
		root := start.DataRoot
		if kind == "resource" {
			root = start.ResourceRoot
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		raw := files[key]
		if raw == nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal("synthetic file removal failed")
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, *raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func nativeAssessmentProjection(value cataloginput.Value) any {
	result := map[string]any{"kind": value.Kind}
	switch value.Kind {
	case "string", "int", "float", "date", "datetime":
		result["text"] = value.Text
	case "bytes":
		result["text"] = base64.StdEncoding.EncodeToString(value.Bytes)
	case "bool":
		result["value"] = value.Boolean
	case "sequence":
		items := make([]any, 0)
		for _, item := range value.Items {
			items = append(items, nativeAssessmentProjection(item))
		}
		result["items"] = items
	case "mapping":
		entries := make([]any, 0)
		for _, entry := range value.Entries {
			entries = append(entries, []any{nativeAssessmentProjection(entry.Key), nativeAssessmentProjection(entry.Value)})
		}
		result["entries"] = entries
	}
	return result
}
func compareNativeAssessment(t *testing.T, expected json.RawMessage, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if json.Unmarshal(raw, &got) != nil || json.Unmarshal(expected, &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("native source mismatch: %.1800s / %.1800s", raw, expected)
	}
}
func TestAssessmentProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_ASSESSMENT_HELPER") != "1" {
		return
	}
	var request assessmentRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 8<<20)).Decode(&request) != nil {
		t.Fatal("invalid synthetic request")
	}
	var w *Workspace
	var err error
	if request.Mode == "unowned" {
		w, err = Open(request.Start)
	} else {
		w, err = OpenForProcess(request.Start)
	}
	if err != nil {
		t.Fatal("synthetic workspace admission failed")
	}
	base := filepath.Dir(request.Start.DataRoot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if request.Mode == "cache-vectors" {
		for _, test := range request.CacheCases {
			installCacheFixture(t, request.Start, test.Files)
			t.Run(test.Name, func(t *testing.T) {
				before := tree(t, base)
				got, failure := w.AssessmentCacheIdentity(ctx, test.Input)
				if failure != nil && got != (assessmentcontext.CacheIdentity{}) {
					t.Fatal("partial native cache identity")
				}
				switch {
				case test.Files["resource:src/data/skill_groups.yaml"] == nil || test.Files["resource:src/data/job_technologies.yaml"] == nil:
					requireCode(t, failure, "SIDECAR_RESOURCE_INVALID")
				case test.Unsupported:
					requireCode(t, failure, "ASSESSMENT_INPUT_UNSUPPORTED")
				case test.Error != "":
					if failure == nil {
						t.Fatal("native cache accepted source serialization failure")
					}
				default:
					if failure != nil || got != test.Expected {
						t.Fatalf("native v2 mismatch: %v", failure)
					}
				}
				if !reflect.DeepEqual(before, tree(t, base)) {
					t.Fatal("native cache identity wrote files")
				}
			})
		}
		return
	}
	if request.Mode == "corpus" {
		for _, test := range request.Cases {
			installAssessmentFixture(t, request.Start, test.Files)
			t.Run(test.Name, func(t *testing.T) {
				before := tree(t, base)
				for _, operation := range []string{"projection", "candidate", "context", "combined"} {
					var value any
					var failure error
					month := assessmentcontext.Month{Year: test.Year, Number: test.Month}
					switch operation {
					case "projection":
						var projection cataloginput.Value
						projection, failure = w.AssessmentCandidateProjection(ctx)
						if failure == nil {
							value = nativeAssessmentProjection(projection)
						} else if !reflect.DeepEqual(projection, cataloginput.Value{}) {
							t.Fatal("partial native projection")
						}
					case "candidate":
						value, failure = w.AssessmentCandidateRevision(ctx)
					case "context":
						value, failure = w.AssessmentContextRevision(ctx, month)
					case "combined":
						value, failure = w.AssessmentRevisions(ctx, month)
					}
					if test.Files["resource:src/data/job_technologies.yaml"] == nil {
						requireCode(t, failure, "SIDECAR_RESOURCE_INVALID")
						continue
					}
					if test.Unsupported && operation != "context" {
						requireCode(t, failure, "ASSESSMENT_INPUT_UNSUPPORTED")
						continue
					}
					if operation == "combined" {
						if test.Expected["candidate"].Error != "" || test.Expected["context"].Error != "" {
							if failure == nil || !reflect.DeepEqual(value, assessmentcontext.Revisions{}) {
								t.Fatal("partial native combined result")
							}
							continue
						}
						if failure != nil {
							t.Fatalf("combined %s: %v", test.Name, failure)
						}
						got := value.(assessmentcontext.Revisions)
						compareNativeAssessment(t, test.Expected["candidate"].Value, got.Candidate)
						compareNativeAssessment(t, test.Expected["context"].Value, got.Context)
						continue
					}
					outcome := test.Expected[operation]
					if outcome.Error != "" {
						if failure == nil {
							t.Fatal("source rejection became native success")
						}
						continue
					}
					if failure != nil {
						t.Fatalf("%s/%s: %v", test.Name, operation, failure)
					}
					compareNativeAssessment(t, outcome.Value, value)
				}
				if !reflect.DeepEqual(before, tree(t, base)) {
					t.Fatal("native assessment changed synthetic files")
				}
			})
		}
		return
	}
	want := ""
	switch request.Mode {
	case "unowned":
		want = "ASSESSMENT_OWNERSHIP_REQUIRED"
	case "identity":
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker("dev", "dh_22222222222222222222222222222222"))
		want = "DATA_HOME_IDENTITY_CHANGED"
	case "recovery":
		writeFixture(t, request.Start.DataRoot, datahome.DocumentRecoveryPath, "synthetic recovery")
		want = "DATA_HOME_RECOVERY_REQUIRED"
	case "resources":
		writeFixture(t, request.Start.ResourceRoot, "VERSION", "invalid")
		want = "SIDECAR_RESOURCE_INVALID"
	case "canceled":
		cancel()
		want = "ASSESSMENT_INPUT_CANCELED"
	case "directory":
		if err := os.MkdirAll(filepath.Join(request.Start.DataRoot, "career_vault", "experience_blocks.yaml"), 0700); err != nil {
			t.Fatal(err)
		}
		want = "ASSESSMENT_INPUT_READ_FAILED"
	}
	before := tree(t, base)
	value, failure := w.AssessmentRevisions(ctx, assessmentcontext.Month{Year: 2026, Number: 9})
	if want != "" {
		requireCode(t, failure, want)
		if !reflect.DeepEqual(value, assessmentcontext.Revisions{}) {
			t.Fatal("partial failure")
		}
	} else if failure != nil || value.Candidate == "" || value.Context == "" {
		t.Fatal("missing native revisions")
	}
	cacheValue, cacheFailure := w.AssessmentCacheIdentity(ctx, nativeCacheInput())
	if want != "" {
		requireCode(t, cacheFailure, want)
		if cacheValue != (assessmentcontext.CacheIdentity{}) {
			t.Fatal("partial v2 native failure")
		}
	} else if cacheFailure != nil || cacheValue.Key == "" {
		t.Fatal("missing native v2 key")
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("native assessment wrote files")
	}
	if request.Mode == "refresh" {
		writeFixture(t, request.Start.DataRoot, "career_vault/experience_blocks.yaml", "[{id: updated}]\n")
		next, failure := w.AssessmentRevisions(ctx, assessmentcontext.Month{Year: 2026, Number: 9})
		if failure != nil || next.Candidate == value.Candidate || next.Context != value.Context {
			t.Fatal("fresh candidate revision failed")
		}
		nextCache, cacheFailure := w.AssessmentCacheIdentity(ctx, nativeCacheInput())
		if cacheFailure != nil || nextCache.Candidate == cacheValue.Candidate || nextCache.Context != cacheValue.Context || nextCache.Key == cacheValue.Key {
			t.Fatal("fresh v2 candidate invalidation failed")
		}
		writeFixture(t, request.Start.DataRoot, "career_vault/taxonomy/skill_groups.yaml", "[{id: changed, name: Changed}]")
		groupCache, cacheFailure := w.AssessmentCacheIdentity(ctx, nativeCacheInput())
		if cacheFailure != nil || groupCache.Candidate != nextCache.Candidate || groupCache.Context == nextCache.Context || groupCache.Key == nextCache.Key {
			t.Fatal("fresh v2 group invalidation failed")
		}
	}
}
