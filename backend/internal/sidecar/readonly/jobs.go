package readonly

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/runtimepaths"
)

type JobPage struct {
	Items      []map[string]any `json:"items"`
	Total      int              `json:"total"`
	NextCursor *string          `json:"next_cursor"`
}

// withJobDirectory pins the normalized directory and admission through the
// complete read. No directory/file is created by browsing the job library.
func withJobDirectory[T any](w *Workspace, ctx context.Context, load func(*runtimepaths.Root, []runtimepaths.FileMetadata) (T, error)) (T, error) {
	return withJobSources(w, ctx, func(data, resources *runtimepaths.Root, entries []runtimepaths.FileMetadata) (T, error) {
		return load(data, entries)
	})
}

func withJobSources[T any](w *Workspace, ctx context.Context, load func(*runtimepaths.Root, *runtimepaths.Root, []runtimepaths.FileMetadata) (T, error)) (T, error) {
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (result T, failure error) {
		var empty T
		present, e := data.Exists("jobs/normalized")
		if e != nil {
			return empty, fail("JOB_LIBRARY_UNAVAILABLE")
		}
		if !present {
			return load(data, resources, []runtimepaths.FileMetadata{})
		}
		directory, e := runtimepaths.Open(filepath.Join(w.start.DataRoot, "jobs", "normalized"))
		if e != nil {
			return empty, fail("JOB_LIBRARY_UNAVAILABLE")
		}
		defer func() {
			if directory.Close() != nil {
				result, failure = empty, fail("JOB_LIBRARY_UNAVAILABLE")
			}
		}()
		before, e := directory.ListFileMetadata()
		if e != nil {
			return empty, fail("JOB_LIBRARY_UNAVAILABLE")
		}
		cards := []runtimepaths.FileMetadata{}
		for _, entry := range before {
			if strings.HasSuffix(strings.ToLower(entry.Name), ".yaml") {
				cards = append(cards, entry)
			}
		}
		result, failure = load(data, resources, cards)
		after, e := directory.ListFileMetadata()
		if e != nil || !slices.EqualFunc(before, after, func(a, b runtimepaths.FileMetadata) bool {
			return a.Name == b.Name && a.Size == b.Size && a.Modified.Equal(b.Modified)
		}) {
			return empty, fail("JOB_LIBRARY_CHANGED")
		}
		return result, failure
	})
}

func projectedJob(ctx context.Context, root *runtimepaths.Root, entry runtimepaths.FileMetadata) (*jobcard.Loaded, map[string]any, error) {
	relative := "jobs/normalized/" + entry.Name
	// Python's isoformat keeps six microsecond digits only when nonzero.
	modified := entry.Modified.UTC().Round(time.Microsecond)
	layout := "2006-01-02T15:04:05.000000+00:00"
	if modified.Nanosecond()/1000 == 0 {
		layout = "2006-01-02T15:04:05+00:00"
	}
	stamp := modified.Format(layout)
	loaded, problem := jobcard.Read(ctx, root, relative)
	if problem != nil {
		if problem.Code == "JOB_CARD_DECODE_FAILED" || problem.Code == "JOB_CARD_SOURCE_ENCODING" {
			return nil, jobcard.InvalidProjection(relative, stamp), nil
		}
		return nil, nil, fail(problem.Code)
	}
	item, problem := jobcard.Project(ctx, loaded, relative, stamp)
	if problem != nil {
		return nil, nil, fail(problem.Code)
	}
	if item["valid"] == true {
		item["card_revision"] = "sha256:" + hex.EncodeToString(loaded.CardFileSHA256[:])
	}
	return loaded, item, nil
}

func (w *Workspace) SavedJobs(ctx context.Context, offset int64, limit int) (*JobPage, error) {
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, fail("JOB_QUERY_INVALID")
	}
	page, err := withJobDirectory(w, ctx, func(root *runtimepaths.Root, entries []runtimepaths.FileMetadata) (*JobPage, error) {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Modified.Equal(entries[j].Modified) {
				return entries[i].Name < entries[j].Name
			}
			return entries[i].Modified.After(entries[j].Modified)
		})
		page := &JobPage{Items: []map[string]any{}, Total: len(entries)}
		if offset >= int64(len(entries)) {
			return page, nil
		}
		end := min(offset+int64(limit), int64(len(entries)))
		bytes := 0
		for _, entry := range entries[offset:end] {
			_, item, err := projectedJob(ctx, root, entry)
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(item)
			if err != nil {
				return nil, fail("JOB_LIBRARY_UNAVAILABLE")
			}
			bytes += len(raw)
			if bytes > 4<<20 {
				return nil, fail("JOB_QUERY_TOO_BROAD")
			}
			page.Items = append(page.Items, item)
		}
		if end < int64(len(entries)) {
			cursor := "offset:" + strconv.FormatInt(end, 10)
			page.NextCursor = &cursor
		}
		return page, nil
	})
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, item := range page.Items {
		if item["valid"] == true {
			ids = append(ids, item["id"].(string))
		}
	}
	summaries, err := w.ApplicationSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, item := range page.Items {
		item["application"] = nil
		if item["valid"] == true {
			if summary, ok := summaries[item["id"].(string)]; ok {
				item["application"] = summary
			}
		}
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return nil, fail("JOB_LIBRARY_UNAVAILABLE")
	}
	if len(raw) > 4<<20 {
		return nil, fail("JOB_QUERY_TOO_BROAD")
	}
	return page, nil
}

type SavedJob struct {
	Loaded *jobcard.Loaded
	Card   map[string]any
}

func (w *Workspace) SavedJob(ctx context.Context, id string) (*SavedJob, error) {
	if !jobcard.ValidID(id) {
		return nil, fail("JOB_CARD_NOT_FOUND")
	}
	return withJobDirectory(w, ctx, func(root *runtimepaths.Root, entries []runtimepaths.FileMetadata) (*SavedJob, error) {
		for _, entry := range entries {
			if entry.Name != id+".yaml" {
				continue
			}
			loaded, item, err := projectedJob(ctx, root, entry)
			if err != nil {
				return nil, err
			}
			if loaded == nil || item["valid"] != true || item["id"] != id {
				return nil, fail("JOB_CARD_INVALID")
			}
			return &SavedJob{Loaded: loaded, Card: item}, nil
		}
		return nil, fail("JOB_CARD_NOT_FOUND")
	})
}

func (w *Workspace) SavedJobSource(ctx context.Context, id string) (json.RawMessage, error) {
	job, err := w.SavedJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if !job.Loaded.RawPresent {
		return nil, fail("JOB_CARD_NOT_FOUND")
	}
	raw, err := json.Marshal(map[string]any{"id": id, "source_name": filepath.Base(job.Card["raw_jd_path"].(string)), "source_url": job.Card["source_url"], "source_text": strings.TrimPrefix(job.Loaded.RawText, "\ufeff")})
	if err != nil {
		return nil, fail("JOB_LIBRARY_UNAVAILABLE")
	}
	return raw, nil
}
