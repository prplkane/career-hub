//go:build windows

package assessment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/discovery/discoverypreview"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/previewcache"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(raw []byte) (int, error) {
	if b.Len()+len(raw) > 4<<20 {
		return 0, errors.New("synthetic test output limit")
	}
	return b.Buffer.Write(raw)
}

type nativeRequest struct {
	Start  protocol.StartRecord
	Target json.RawMessage
}

func snapshot(t *testing.T, base string, omitCache bool) map[string]string {
	t.Helper()
	cachePath, _ := previewcache.Path("synthetic")
	allowed := map[string]bool{"data/cache": true, "data/cache/go": true, "data/" + previewcache.Directory: true, "data/" + cachePath: true}
	result := map[string]string{}
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if omitCache && allowed[relative] {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(":%d:", info.ModTime().UnixNano()) + string(raw)
		}
		result[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func rewrite(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAssessmentNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_ASSESSMENT_HELPER") != "1" {
		return
	}
	var request nativeRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 4<<20)).Decode(&request) != nil {
		t.Fatal("invalid synthetic request")
	}
	start := request.Start
	if s, err := Open(start); s != nil || err == nil {
		t.Fatal("unowned service admitted")
	}
	if _, err := readonly.OpenForProcess(start); err != nil {
		t.Fatal(err)
	}
	s, err := Open(start)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(start.DataRoot)
	before := snapshot(t, base, true)
	ctx := context.Background()
	month := assessmentcontext.Month{Year: 2026, Number: 9}
	result, err := s.Assess(ctx, " synthetic ", month)
	if err != nil || result.Preview == nil || result.CacheStatus != "missing" || !result.Write.Committed || !result.Write.DirectoriesCreated {
		t.Fatalf("initial publication: %v / %v", result, err)
	}
	var got, want any
	raw, _ := json.Marshal(result.Preview.Prepared.Artifact)
	if json.Unmarshal(raw, &got) != nil || json.Unmarshal(request.Target, &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("native persisted preview differs from actual Python source")
	}
	if !reflect.DeepEqual(before, snapshot(t, base, true)) {
		t.Fatal("Go publication changed Python cache/history, private files, resources or unexpected paths")
	}
	full := snapshot(t, base, false)
	s.now = func() time.Time { t.Fatal("cache hit tried to persist"); return time.Time{} }
	result, err = s.Assess(ctx, "synthetic", month)
	if err != nil || !result.Preview.CacheHit || result.CacheStatus != "hit" || result.Write != (previewcache.WriteReport{}) {
		t.Fatal("native qualified hit failed")
	}
	if !reflect.DeepEqual(full, snapshot(t, base, false)) {
		t.Fatal("cache hit rewrote files")
	}
	result, err = s.Assess(ctx, "partial", month)
	if err != nil || result.Preview == nil || result.Preview.Prepared != nil || result.CacheStatus != "ineligible" || result.Write != (previewcache.WriteReport{}) {
		t.Fatal("partial capture acquired an assessment")
	}
	if !reflect.DeepEqual(full, snapshot(t, base, false)) {
		t.Fatal("partial capture wrote cache")
	}
	s.now = time.Now
	month.Number++
	result, err = s.Assess(ctx, "synthetic", month)
	if err != nil || result.CacheStatus != "stale" || !result.Write.Committed || result.Write.DirectoriesCreated {
		t.Fatal("month change failed to replace stale cache")
	}
	cachePath, _ := previewcache.Path("synthetic")
	cachePath = filepath.Join(start.DataRoot, filepath.FromSlash(cachePath))
	rewrite(t, cachePath, []byte("invalid old cache"))
	result, err = s.Assess(ctx, "synthetic", month)
	if err != nil || result.CacheStatus != "invalid" || !result.Write.Committed {
		t.Fatal("corrupt cache not recomputed")
	}

	// Every post-commit path must retain the committed fact. A synthetic fault
	// wraps the real native publisher, not a pretend successful write.
	for _, mode := range []string{"postcommit-error", "postcommit-cancel", "stale-observation"} {
		month.Number++
		if month.Number > 12 {
			month.Number = 1
			month.Year++
		}
		callCtx, cancel := context.WithCancel(ctx)
		s.save = func(c context.Context, root *runtimepaths.Root, observed previewcache.Observation, id string, identity assessmentcontext.CacheIdentity, prepared *discoverypreview.Prepared, computed time.Time) (previewcache.WriteReport, error) {
			if mode == "stale-observation" {
				rewrite(t, cachePath, []byte("concurrent cache edit"))
			}
			report, err := previewcache.SaveOwned(c, root, observed, id, identity, prepared, computed)
			if mode == "postcommit-error" && report.Committed {
				return report, previewcache.ErrWrite
			}
			if mode == "postcommit-cancel" {
				cancel()
			}
			return report, err
		}
		result, err = s.Assess(callCtx, "synthetic", month)
		cancel()
		switch mode {
		case "postcommit-error":
			requireFailure(t, result, err, "ASSESSMENT_CACHE_SAVE_FAILED", true)
		case "postcommit-cancel":
			if err != nil || result.Preview == nil || !result.Write.Committed {
				t.Fatal("late cancellation erased committed publication")
			}
		case "stale-observation":
			requireFailure(t, result, err, "ASSESSMENT_CACHE_CHANGED", false)
			if result.Write.Attempted || string(fileBytes(t, cachePath)) != "concurrent cache edit" {
				t.Fatal("stale observation overwritten")
			}
		}
	}
	s.save = previewcache.SaveOwned
	result, err = s.Assess(ctx, "synthetic", month)
	if err != nil || !result.Write.Committed {
		t.Fatal("cache repair failed")
	}
	if !reflect.DeepEqual(before, snapshot(t, base, true)) {
		t.Fatal("Go hit/miss/replace/failure paths modified original Python files or SQLite history")
	}
	// Current full Vault validity is required even when the excluded style
	// document does not participate in the v2 cache identity.
	style := filepath.Join(start.DataRoot, "career_vault", "style_rules.yaml")
	original := fileBytes(t, style)
	rewrite(t, style, []byte("invalid"))
	full = snapshot(t, base, false)
	result, err = s.Assess(ctx, "synthetic", month)
	requireFailure(t, result, err, "ASSESSMENT_PREVIEW_UNAVAILABLE", false)
	if !reflect.DeepEqual(full, snapshot(t, base, false)) {
		t.Fatal("invalid context changed cache")
	}
	rewrite(t, style, original)
	// Cache I/O failure is not silently downgraded to a fresh successful result.
	oldCache := fileBytes(t, cachePath)
	if err := os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cachePath, 0700); err != nil {
		t.Fatal(err)
	}
	full = snapshot(t, base, false)
	result, err = s.Assess(ctx, "synthetic", month)
	requireFailure(t, result, err, "ASSESSMENT_PREVIEW_UNAVAILABLE", false)
	if !reflect.DeepEqual(full, snapshot(t, base, false)) {
		t.Fatal("cache read failure wrote files")
	}
	if err := os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	rewrite(t, cachePath, oldCache)

	markerPath := filepath.Join(start.DataRoot, datahome.MarkerPath)
	originalMarker := fileBytes(t, markerPath)
	month.Number++
	s.save = func(c context.Context, root *runtimepaths.Root, observed previewcache.Observation, id string, identity assessmentcontext.CacheIdentity, prepared *discoverypreview.Prepared, computed time.Time) (previewcache.WriteReport, error) {
		report, err := previewcache.SaveOwned(c, root, observed, id, identity, prepared, computed)
		if report.Committed {
			rewrite(t, markerPath, []byte(strings.ReplaceAll(string(originalMarker), "dh_0123456789abcdef0123456789abcdef", "dh_22222222222222222222222222222222")))
		}
		return report, err
	}
	result, err = s.Assess(ctx, "synthetic", month)
	requireFailure(t, result, err, "ASSESSMENT_ADMISSION_CHANGED", true)
	rewrite(t, markerPath, originalMarker)
}
