package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/textcompat"
)

// FeedRequest selects only fixed discovery operations, never a file or SQL query.
type FeedRequest struct {
	Operation, ID, State string
	Action               string
	Page                 discovery.PageOptions
	Revision             any
	Overrides            map[string]any
}

func feedRoute(r *http.Request) (FeedRequest, bool) {
	if r.URL == nil {
		return FeedRequest{}, false
	}
	path := r.URL.EscapedPath()
	if path == "/api/discoveries" && r.Method == "GET" {
		return FeedRequest{Operation: "list"}, true
	}
	if (path == "/api/discoveries/flush-archived" && (r.Method == "POST" || r.Method == "DELETE")) || (path == "/api/discoveries/archived" && r.Method == "DELETE") {
		return FeedRequest{Operation: "flush"}, true
	}
	if !strings.HasPrefix(path, "/api/discoveries/") {
		return FeedRequest{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/discoveries/"), "/")
	op := ""
	if len(parts) == 1 {
		switch r.Method {
		case "GET":
			op = "detail"
		case "PUT":
			op = "transition"
		case "DELETE":
			op = "delete"
		}
	} else if len(parts) == 2 {
		if r.Method == "PUT" && parts[1] == "field-overrides" {
			op = "overrides"
		}
		if r.Method == "POST" && parts[1] == "confirm-content" {
			op = "confirm"
		}
		if r.Method == "POST" && parts[1] == "promote" {
			op = "promote"
		}
		if r.Method == "POST" && parts[1] == "resolve-public-link" {
			op = "resolve_public"
		}
	}
	if op == "" {
		return FeedRequest{}, false
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || id == "." || id == ".." || strings.ContainsFunc(id, func(c rune) bool { return c < 32 || c == 127 }) {
		return FeedRequest{}, false
	}
	id, problem := discovery.NormalizeID(id)
	if problem != nil || id == "email-imports" || ((op == "promote" || op == "resolve_public") && parts[0] != id) {
		return FeedRequest{}, false
	}
	return FeedRequest{Operation: op, ID: id}, true
}

func feedPage(raw string) (discovery.PageOptions, bool) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return discovery.PageOptions{}, false
	}
	first := func(key, fallback string) string {
		for _, v := range query[key] {
			if v != "" {
				return v
			}
		}
		return fallback
	}
	list := func(key string) []string {
		values, seen := []string{}, map[string]bool{}
		for _, value := range query[key] {
			for _, item := range strings.Split(value, ",") {
				item = textcompat.Strip(item)
				if item != "" && !seen[item] {
					values = append(values, item)
					seen[item] = true
				}
			}
		}
		return values
	}
	limit, e := strconv.Atoi(textcompat.Strip(first("limit", "20")))
	cursor := first("cursor", "offset:0")
	if e != nil || limit < 1 || limit > 100 || !strings.HasPrefix(cursor, "offset:") {
		return discovery.PageOptions{}, false
	}
	offset, e := strconv.ParseInt(textcompat.Strip(strings.TrimPrefix(cursor, "offset:")), 10, 64)
	if e != nil || offset < 0 {
		return discovery.PageOptions{}, false
	}
	page := discovery.PageOptions{Filters: discovery.Filters{States: list("state"), SourceKinds: list("source_kind"), Readiness: list("readiness"), Queues: list("queue"), Search: first("q", ""), ObservedAfter: first("observed_after", "")}, Sort: first("sort", "last_desc"), Limit: limit, Offset: offset}
	return page, discovery.ValidatePage(page) == nil
}

func (h *handler) serveFeed(w http.ResponseWriter, r *http.Request, request FeedRequest) {
	if (request.Operation == "promote" || request.Operation == "resolve_public") && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
		applicationFailure(w, &ApplicationOutcome{}, "DISCOVERY_UPDATE_INVALID")
		return
	}
	if request.Operation == "list" {
		var ok bool
		request.Page, ok = feedPage(r.URL.RawQuery)
		if !ok {
			applicationFailure(w, &ApplicationOutcome{}, "DISCOVERY_QUERY_INVALID")
			return
		}
	} else if request.Operation != "detail" {
		fields := map[string]json.RawMessage{}
		if r.ContentLength != 0 || (request.Operation != "delete" && request.Operation != "flush") {
			var code string
			fields, code, _ = materialJSONChecked(r, func(raw []byte) bool {
				return applicationScalarJSON(raw) && (request.Operation != "promote" && request.Operation != "resolve_public" || uniqueJobDetailsJSON(raw))
			})
			if code != "" {
				applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
				return
			}
		}
		valid := false
		switch request.Operation {
		case "transition":
			valid = materialKeys(fields, "state", "expected_revision") && json.Unmarshal(fields["state"], &request.State) == nil && string(fields["state"]) != "null"
		case "overrides":
			valid = materialKeys(fields, "contract_version", "expected_override_revision", "overrides") && string(fields["contract_version"]) == "1" && json.Unmarshal(fields["overrides"], &request.Overrides) == nil && request.Overrides != nil
		case "confirm":
			valid = materialKeys(fields, "contract_version", "expected_revision") && string(fields["contract_version"]) == "1"
		case "resolve_public":
			valid = len(fields) == 2 && materialKeys(fields, "contract_version", "expected_revision") && string(fields["contract_version"]) == "1"
		case "promote":
			valid = len(fields) == 3 && materialKeys(fields, "contract_version", "action", "expected_revision") && string(fields["contract_version"]) == "1" && json.Unmarshal(fields["action"], &request.Action) == nil &&
				(request.Action == "save_job" || request.Action == "prepare_application" || request.Action == "record_as_applied")
		case "delete":
			valid = len(fields) == 0 || materialKeys(fields, "expected_revision")
		case "flush":
			valid = len(fields) == 0
		}
		key := "expected_revision"
		if request.Operation == "overrides" {
			key = "expected_override_revision"
		}
		if value, found := fields[key]; found {
			if string(value) == "null" && request.Operation == "delete" {
				request.Revision = nil
			} else {
				var revision json.Number
				if len(value) == 0 || value[0] == '"' || json.Unmarshal(value, &revision) != nil || strings.ContainsAny(string(revision), ".eE") {
					valid = false
				} else {
					n, err := strconv.ParseInt(string(revision), 10, 64)
					if err != nil || n < 0 || (n == 0 && request.Operation != "overrides") {
						valid = false
					} else {
						request.Revision = revision
					}
				}
			}
		}
		if !valid {
			applicationFailure(w, &ApplicationOutcome{}, "DISCOVERY_UPDATE_INVALID")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if request.Operation == "resolve_public" {
		h.applicationCallUnlocked(w, ctx, 200, func() (*ApplicationOutcome, *ApplicationError) { return h.application.Feed(ctx, request) })
		return
	}
	h.applicationCall(w, ctx, 200, func() (*ApplicationOutcome, *ApplicationError) { return h.application.Feed(ctx, request) })
}
