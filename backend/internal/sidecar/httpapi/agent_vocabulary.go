package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/emaildiagnostics"
	"careerhub.local/backend/internal/textcompat"
)

type ExactVocabularyRequest = catalogwrite.ExactVocabularyRequest

// Never serialize these objects directly: each may retain domain results.
type VocabularyStages struct{ Registry, Reanalysis *ApplicationOutcome }

func applicationOperationState(out *ApplicationOutcome) map[string]any {
	state := map[string]any{"known": out != nil, "committed": nil, "partial": nil, "cleanup_failed": nil}
	if out != nil {
		state["committed"], state["partial"], state["cleanup_failed"] = out.Committed, out.Partial, out.CleanupFailed
		if out.CommitUnknown {
			state["known"] = false
			if !out.Committed {
				state["committed"] = nil
			}
		}
		if out.RecoveryRequired {
			state["recovery_required"] = true
		}
		if out.RevisionRefreshRequired {
			state["revision_refresh_required"] = true
		}
		if out.Write != nil {
			state["write"] = out.Write
		}
	}
	return state
}

func vocabularyStageState(stages *VocabularyStages) map[string]any {
	registry := applicationOperationState(stages.Registry)
	var reanalysis any
	if stages.Reanalysis != nil {
		r := applicationOperationState(stages.Reanalysis)
		if emaildiagnostics.ValidRunID(stages.Reanalysis.RunID) {
			r["run_id"] = stages.Reanalysis.RunID
		}
		reanalysis = r
	}
	return map[string]any{"registry": registry, "reanalysis": reanalysis}
}

func (h *handler) serveExactVocabulary(w http.ResponseWriter, r *http.Request, jobID string) {
	if h.application.ExactVocabulary == nil {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	invalid := func() { applicationFailure(w, &ApplicationOutcome{}, "AGENT_VOCABULARY_REQUEST_INVALID") }
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		invalid()
		return
	}
	fields, code, _ := materialJSONCheckedLimit(r, func(raw []byte) bool { return applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw) }, 4096)
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	request := ExactVocabularyRequest{JobID: jobID}
	var groupID *string
	if !materialKeys(fields, "name", "primary_group_id", "source_line", "confirmed_exact") ||
		json.Unmarshal(fields["name"], &request.Name) != nil || textcompat.Strip(request.Name) == "" || utf8.RuneCountInString(textcompat.Strip(request.Name)) > 160 ||
		json.Unmarshal(fields["primary_group_id"], &groupID) != nil || groupID == nil || len(*groupID) > 160 ||
		json.Unmarshal(fields["confirmed_exact"], &request.ConfirmedExact) != nil || !request.ConfirmedExact {
		invalid()
		return
	}
	request.PrimaryGroupID = *groupID
	if raw, exists := fields["source_line"]; exists {
		var line int
		if json.Unmarshal(raw, &line) != nil || line < 1 || line > 200000 {
			invalid()
			return
		}
		request.SourceLine = &line
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		out, problem := h.application.ExactVocabulary(ctx, request)
		if problem != nil || out == nil || ctx.Err() != nil {
			return out, problem
		}
		// Stage projections expose facts, not the retained raw JD or journal.
		var receipt map[string]json.RawMessage
		if out.VocabularyStages == nil || len(out.Result) > MaxResponseBytes-8192 || json.Unmarshal(out.Result, &receipt) != nil || receipt == nil {
			return out, &ApplicationError{Code: "AGENT_VOCABULARY_RESULT_INVALID"}
		}
		receipt["stages"], _ = json.Marshal(vocabularyStageState(out.VocabularyStages))
		raw, err := json.Marshal(receipt)
		if err != nil {
			return out, &ApplicationError{Code: "AGENT_VOCABULARY_RESULT_INVALID"}
		}
		out.Result = raw
		return out, nil
	})
}
