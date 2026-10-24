package agentcontext

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"careerhub.local/backend/internal/applications"
)

// ResumeContextInput is an observed active library and optional application
// summary. SQL filtering/order remains owned by applications.ReadResumeLibrary.
// Warning text must already be public-safe; never pass a raw private exception.
type ResumeContextInput struct {
	Initialized    bool
	Library        json.RawMessage
	Application    json.RawMessage
	LibraryFailure *string
	IncludeLibrary bool
	IncludeDigest  bool
	TargetID       *string
}

// ResumeDigestReader receives only selected {id,display_name} rows, not the full
// library or private identities. The owner may enrich them from its guarded
// snapshot. A public warning falls back to the original bounded rows. A fatal
// cancellation/coherence error refuses the whole context. No reader is called
// for assessment/writing or an empty selection.
type ResumeDigestReader func(context.Context, json.RawMessage) (json.RawMessage, *string, *Error)

type ResumeContext struct {
	SelectedID     any              `json:"selected_resume_id"`
	TargetID       any              `json:"target_resume_id"`
	Items          []map[string]any `json:"items"`
	Total          int              `json:"total"`
	Truncated      bool             `json:"truncated"`
	DigestIncluded bool             `json:"digest_status_included"`
	Warning        *string          `json:"warning,omitempty"`
}

// SQLite scalar IDs are uncommon but already supported by the ledger reader.
// Match Python str without database/sql coercion, including integral REAL .0,
// fixed/scientific thresholds and null. Container IDs are not SQLite scalars.
func resumeScalarKey(value any) (string, bool, *Error) {
	switch v := value.(type) {
	case nil:
		return "None", false, nil
	case string:
		return v, v != "", nil
	case bool:
		if v {
			return "True", true, nil
		}
		return "False", false, nil
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			if v == "-0" {
				return "0", false, nil
			}
			return string(v), v != "0", nil
		}
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return "", false, failure("RESUME_INPUT")
		}
		e := strconv.FormatFloat(f, 'e', -1, 64)
		i := strings.IndexByte(e, 'e')
		exponent, _ := strconv.Atoi(e[i+1:])
		if exponent >= -4 && exponent < 16 {
			e = strconv.FormatFloat(f, 'f', -1, 64)
			if !strings.Contains(e, ".") {
				e += ".0"
			}
		}
		return e, f != 0, nil
	default:
		return "", false, failure("RESUME_INPUT")
	}
}
func resumeWarning(ctx context.Context, prefix, text string) (*string, *Error) {
	budget := contextJSONBudget{ctx: ctx, maximum: 32768}
	if err := budget.check(text, 0); err != nil {
		return nil, err
	}
	message := prefix + text
	return &message, nil
}

func normalizedResumeScalar(value any) (any, *Error) {
	if _, number := value.(json.Number); number {
		key, _, err := resumeScalarKey(value)
		if err != nil {
			return nil, err
		}
		return json.Number(key), nil
	}
	return value, nil
}

func BuildResumeContext(ctx context.Context, input ResumeContextInput, digest ResumeDigestReader) (*ResumeContext, *Error) {
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	out := &ResumeContext{Items: []map[string]any{}}
	if input.TargetID != nil {
		out.TargetID = *input.TargetID
	}
	if !input.Initialized || input.LibraryFailure != nil {
		message := "Resume Library is not initialized."
		if input.Initialized {
			var err *Error
			out.Warning, err = resumeWarning(ctx, "Resume Version status unavailable: ", *input.LibraryFailure)
			if err != nil {
				return nil, err
			}
		} else {
			out.Warning = &message
		}
		return out, nil
	}
	if len(input.Library) > applications.MaxResultBytes {
		return nil, failure("RESUME_LIMIT")
	}
	var resumes []map[string]any
	if !json.Valid(input.Library) || decodeNumbers(input.Library, &resumes) != nil || resumes == nil || len(resumes) > 10000 {
		return nil, failure("RESUME_INPUT")
	}
	budget := contextJSONBudget{ctx: ctx, maximum: applications.MaxResultBytes}
	for _, row := range resumes {
		if row == nil {
			return nil, failure("RESUME_INPUT")
		}
		if err := budget.check(row, 0); err != nil {
			return nil, err
		}
		for _, key := range []string{"id", "display_name"} {
			value, err := normalizedResumeScalar(row[key])
			if err != nil {
				return nil, err
			}
			row[key] = value
		}
	}
	if len(input.Application) > 0 && strings.TrimSpace(string(input.Application)) != "null" {
		application, err := decodedContextObject(ctx, input.Application, applications.MaxResultBytes)
		if err != nil {
			return nil, err
		}
		selection, _ := application["resume_selection"].(map[string]any)
		resume, _ := selection["resume"].(map[string]any)
		out.SelectedID = resume["id"]
	}
	var scalarErr *Error
	out.SelectedID, scalarErr = normalizedResumeScalar(out.SelectedID)
	if scalarErr != nil {
		return nil, scalarErr
	}
	selectedKey, selectedTruthy, err := resumeScalarKey(out.SelectedID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(resumes))
	available := map[string]bool{}
	for i, row := range resumes {
		if row == nil {
			return nil, failure("RESUME_INPUT")
		}
		key, _, err := resumeScalarKey(row["id"])
		if err != nil {
			return nil, err
		}
		keys[i] = key
		available[key] = true
	}
	if input.TargetID != nil && !available[*input.TargetID] {
		return nil, failure("RESUME_TARGET_UNAVAILABLE")
	}
	prioritized := []string{}
	if input.TargetID != nil {
		prioritized = append(prioritized, *input.TargetID)
	}
	if selectedTruthy {
		prioritized = append(prioritized, selectedKey)
	}
	priority := map[string]int{}
	for _, id := range prioritized {
		if _, exists := priority[id]; !exists {
			priority[id] = len(priority)
		}
	}
	order := make([]int, len(resumes))
	for i := range order {
		order[i] = i
	}
	rank := func(i int) int {
		if value, exists := priority[keys[i]]; exists {
			return value
		}
		return len(priority)
	}
	sort.SliceStable(order, func(i, j int) bool { return rank(order[i]) < rank(order[j]) })
	bounded := []map[string]any{}
	for _, i := range order {
		_, wanted := priority[keys[i]]
		if input.IncludeLibrary || wanted {
			// This is the small library projection, not an arbitrary metadata copy.
			bounded = append(bounded, map[string]any{"id": resumes[i]["id"], "display_name": resumes[i]["display_name"]})
			if input.IncludeLibrary && len(bounded) == 8 {
				break
			}
		}
	}
	out.Total = len(resumes)
	out.Truncated = input.IncludeLibrary && len(resumes) > len(bounded)
	out.DigestIncluded = input.IncludeDigest
	if input.TargetID == nil || *input.TargetID == "" {
		out.TargetID = out.SelectedID
	}
	if input.IncludeDigest && len(bounded) > 0 {
		if digest == nil {
			return nil, failure("RESUME_DIGEST_READER_REQUIRED")
		}
		raw, _ := json.Marshal(bounded)
		result, warning, err := digest(ctx, raw)
		if err != nil {
			return nil, err
		}
		if err := contextStopped(ctx); err != nil {
			return nil, err
		}
		if warning != nil {
			out.Warning, err = resumeWarning(ctx, "Resume digest status unavailable: ", *warning)
			if err != nil {
				return nil, err
			}
		} else {
			if len(result) > applications.MaxResultBytes {
				return nil, failure("RESUME_LIMIT")
			}
			var enriched []map[string]any
			if !json.Valid(result) || decodeNumbers(result, &enriched) != nil || len(enriched) != len(bounded) {
				return nil, failure("RESUME_DIGEST_IDENTITY")
			}
			for i, row := range enriched {
				if !reflect.DeepEqual(row["id"], bounded[i]["id"]) {
					return nil, failure("RESUME_DIGEST_IDENTITY")
				}
				if err := budget.check(row, 0); err != nil {
					return nil, err
				}
			}
			bounded = enriched
		}
	}
	for _, row := range bounded {
		item := map[string]any{"id": row["id"], "display_name": row["display_name"]}
		if input.IncludeDigest {
			for _, key := range []string{"digest_status", "digest_supported", "digest_processed_at"} {
				item[key] = row[key]
			}
		}
		out.Items = append(out.Items, item)
	}
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
