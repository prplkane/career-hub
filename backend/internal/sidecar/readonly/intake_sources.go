package readonly

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

type IntakeSource struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

type IntakeSourcePage struct {
	Items      []IntakeSource `json:"items"`
	Total      int            `json:"total"`
	NextCursor *string        `json:"next_cursor"`
}

// IntakeSources lists metadata only. Browsing never creates raw storage, reads
// resume contents, or treats an unsafe/unreadable directory as an empty inbox.
func (w *Workspace) IntakeSources(ctx context.Context, offset int64, limit int) (*IntakeSourcePage, error) {
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, fail("INTAKE_QUERY_INVALID")
	}
	return readOwnedAssessment(w, ctx, func(data, _ *runtimepaths.Root) (*IntakeSourcePage, error) {
		before, present, err := data.ListDirectoryFileMetadata("career_vault/raw")
		if err != nil {
			return nil, fail("INTAKE_SOURCES_UNAVAILABLE")
		}
		result := intakeSourcePage(before, offset, limit)
		after, stillPresent, err := data.ListDirectoryFileMetadata("career_vault/raw")
		if err != nil || present != stillPresent || !slices.EqualFunc(before, after, func(a, b runtimepaths.FileMetadata) bool {
			return a.Name == b.Name && a.Size == b.Size && a.Modified.Equal(b.Modified)
		}) {
			return nil, fail("INTAKE_SOURCES_CHANGED")
		}
		raw, encodeErr := json.Marshal(result)
		if encodeErr != nil || len(raw) > maxIntakeViewResponseBytes {
			return nil, fail("INTAKE_VIEW_RESULT_LIMIT")
		}
		return result, nil
	})
}

func intakeSourcePage(entries []runtimepaths.FileMetadata, offset int64, limit int) *IntakeSourcePage {
	sources := make([]IntakeSource, 0, len(entries))
	for _, entry := range entries {
		suffix := strings.ToLower(path.Ext(entry.Name))
		if suffix == ".txt" || suffix == ".docx" {
			sources = append(sources, IntakeSource{entry.Name, "career_vault/raw/" + entry.Name, entry.Size})
		}
	}
	sort.SliceStable(sources, func(i, j int) bool { return textcompat.Fold(sources[i].Name) < textcompat.Fold(sources[j].Name) })
	result := &IntakeSourcePage{Items: []IntakeSource{}, Total: len(sources)}
	if offset >= int64(len(sources)) {
		return result
	}
	end := int(offset) + limit
	if end < len(sources) {
		cursor := "offset:" + strconv.Itoa(end)
		result.NextCursor = &cursor
	} else {
		end = len(sources)
	}
	result.Items = sources[int(offset):end]
	return result
}
