package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"careerhub.local/backend/internal/applicationcontacts"
	"careerhub.local/backend/internal/resumeplan"
)

// Contact creation/claiming is exposed only through the two exact guarded
// candidate workflows below. The ID routes remain existing-ledger editors,
// never arbitrary create/link RPCs.
func contactRoute(r *http.Request) (JobRequest, bool) {
	path := r.URL.EscapedPath()
	if r.Method == http.MethodPost && r.URL.RawQuery == "" && !r.URL.ForceQuery {
		switch path {
		case "/api/contacts/candidate-preview":
			return JobRequest{Operation: "contact.candidate_preview"}, true
		case "/api/contacts/confirm-candidate":
			return JobRequest{Operation: "contact.candidate_confirm"}, true
		}
	}
	if path == "/api/contacts" && r.Method == "GET" {
		return JobRequest{Operation: "contact.list"}, true
	}
	if !strings.HasPrefix(path, "/api/contacts/") {
		return JobRequest{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/contacts/"), "/")
	if len(parts) > 2 {
		return JobRequest{}, false
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || !strings.HasPrefix(id, "contact_") || len(id) != 40 {
		return JobRequest{}, false
	}
	for _, char := range strings.TrimPrefix(id, "contact_") {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return JobRequest{}, false
		}
	}
	op := ""
	if len(parts) == 1 {
		op = map[string]string{"GET": "get", "PUT": "update"}[r.Method]
	} else if r.Method == "POST" && slices.Contains([]string{"archive", "restore", "unlink"}, parts[1]) {
		op = parts[1]
	}
	return JobRequest{Operation: "contact." + op, ID: id}, op != ""
}

func (h *handler) serveJobEditor(w http.ResponseWriter, r *http.Request, request JobRequest) {
	request.Fields = map[string]any{}
	invalid := "CONTACT_REQUEST_INVALID"
	if strings.HasPrefix(request.Operation, "plan.") {
		invalid = "RESUME_PLAN_INVALID"
		request.Plan = &resumeplan.Request{JobID: request.ID, Operation: strings.TrimPrefix(request.Operation, "plan.")}
	}
	if request.Operation == "contact.list" {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			applicationFailure(w, &ApplicationOutcome{}, invalid)
			return
		}
		value := strings.ToLower(query.Get("include_archived"))
		request.Fields["include_archived"] = value == "true" || value == "1"
	}
	if r.Method != "GET" {
		// Resume Plan retains the source's 1 MiB envelope allowance, not the
		// 16 KiB status/checklist ceiling. Domain bounds still apply afterward.
		fields, code, _ := materialJSONChecked(r, applicationScalarJSON)
		if code != "" {
			applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
			return
		}
		valid := false
		if request.Plan != nil {
			valid = planEditorRequest(request.Plan, fields)
		} else {
			valid = contactEditorRequest(&request, fields)
		}
		if !valid {
			applicationFailure(w, &ApplicationOutcome{}, invalid)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.Jobs(ctx, request)
	})
}

func planEditorRequest(request *resumeplan.Request, fields map[string]json.RawMessage) bool {
	if len(fields) != 3 || !materialKeys(fields, "action", "expected_revision", "plan") {
		return false
	}
	var action string
	if json.Unmarshal(fields["action"], &action) != nil || action != "save_draft" && action != "approve" {
		return false
	}
	revision, valid := materialRevision(fields["expected_revision"])
	if !valid || !materialJSONObject(fields["plan"]) {
		return false
	}
	request.ExpectedRevision, request.Approve, request.Payload = revision, action == "approve", fields["plan"]
	return true
}

func contactEditorRequest(request *JobRequest, fields map[string]json.RawMessage) bool {
	keys := []string{"contract_version", "expected_revision"}
	switch request.Operation {
	case "contact.update":
		keys = append(keys, "updates")
	case "contact.unlink":
		keys = append(keys, "job_ids", "discovery_ids")
	case "contact.archive", "contact.restore":
	default:
		return false
	}
	if len(fields) != len(keys) || !materialKeys(fields, keys...) || string(fields["contract_version"]) != "1" {
		return false
	}
	revision, valid := materialRevision(fields["expected_revision"])
	if !valid || revision < 1 {
		return false
	}
	request.Fields["expected_revision"] = revision
	if request.Operation == "contact.update" {
		var ok bool
		request.ContactUpdates, ok = contactFactChanges(fields["updates"])
		return ok
	}
	if request.Operation == "contact.unlink" {
		for _, key := range []string{"job_ids", "discovery_ids"} {
			var values []any
			if json.Unmarshal(fields[key], &values) != nil || values == nil || len(values) > 100 {
				return false
			}
			for _, value := range values {
				if _, ok := value.(string); !ok {
					return false
				}
			}
			request.Fields[key] = values
		}
	}
	return true
}

// Match Python JSON object semantics deliberately: the last value wins while
// a duplicate key keeps its first insertion position. Preserve validation order
// instead of relying on randomized Go map iteration.
func contactFactChanges(raw json.RawMessage) ([]applicationcontacts.FactChange, bool) {
	if !materialJSONObject(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if _, err := d.Token(); err != nil {
		return nil, false
	}
	changes := []applicationcontacts.FactChange{}
	positions := map[string]int{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !slices.Contains([]string{"name", "role", "company", "context"}, key) {
			return nil, false
		}
		var value any
		if d.Decode(&value) != nil {
			return nil, false
		}
		if position, exists := positions[key]; exists {
			changes[position].Value = value
		} else {
			positions[key] = len(changes)
			changes = append(changes, applicationcontacts.FactChange{Field: key, Value: value})
		}
	}
	for _, change := range changes {
		if _, ok := change.Value.(string); !ok {
			return nil, false
		}
	}
	return changes, len(changes) > 0
}
