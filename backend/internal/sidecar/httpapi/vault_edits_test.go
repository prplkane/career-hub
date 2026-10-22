package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func vaultRequest(method, path, token, body string) *http.Request {
	r := materialPost(path, token, body)
	r.Method = method
	return r
}

func TestVaultEditRoutesAndEnvelopes(t *testing.T) {
	h, token, s := applicationFixture(t)
	for _, collection := range vaultEditCollections {
		for _, op := range []string{"create", "update", "archive", "restore"} {
			path, method, id, status := collection.path, "POST", "", 201
			body := `{"expected_revision":"raw-token","record":{"name":"Exact Ω"}}`
			if op != "create" {
				path += "/legacy%20%CE%A9"
				id = "legacy Ω"
				status = 200
				method = "PUT"
			}
			if op == "archive" || op == "restore" {
				method = "POST"
				path += "/" + op
				body = `{"expected_revision":"raw-token"}`
			}
			response(t, h, vaultRequest(method, path, token, body), status)
			if s.calls[len(s.calls)-1] != collection.kind+"."+op+":"+id || s.revision != "raw-token" {
				t.Fatal("incorrect route", path, s.calls)
			}
		}
	}
	response(t, h, vaultRequest("POST", "/api/skills-registry/skills/batch", token, `{"expected_revision":"raw-token","record":{"names":["A","B"]}}`), 201)
	if s.calls[len(s.calls)-1] != "registry_skill.batch:" {
		t.Fatal("batch routed as a record")
	}
	for _, id := range []string{"legacy%25", "%252e"} {
		response(t, h, vaultRequest("PUT", "/api/vault/projects/"+id, token, `{"expected_revision":"raw-token","record":{}}`), 200)
		want := "legacy%"
		if id == "%252e" {
			want = "%2e"
		}
		if s.calls[len(s.calls)-1] != "project.update:"+want {
			t.Fatal("record ID must be decoded exactly once")
		}
	}
	before := len(s.calls)
	for _, body := range []string{`{}`, `{"expected_revision":null,"record":{}}`, `{"expected_revision":1,"record":{}}`, `{"expected_revision":"x","record":null}`, `{"expected_revision":"x","record":[]}`, `{"expected_revision":"x","record":{},"path":"private"}`, `{"expected_revision":"x","record":{"name":"\ud800"}}`} {
		response(t, h, vaultRequest("POST", "/api/vault/projects", token, body), 400)
		response(t, h, vaultRequest("POST", "/api/skills-registry/groups", token, body), 400)
	}
	response(t, h, vaultRequest("POST", "/api/vault/projects/id/archive", token, `{"expected_revision":"x","record":{}}`), 400)
	if len(s.calls) != before {
		t.Fatal("invalid envelope reached owner")
	}
}

func TestVaultEditClosedPathsAndAccess(t *testing.T) {
	h, token, s := applicationFixture(t)
	body := `{"expected_revision":"x","record":{}}`
	for _, path := range []string{"/api/vault/projects/", "/api/vault/projects/a/b", "/api/vault/projects/%2fetc", "/api/vault/projects/%5cfile", "/api/vault/projects/%00", "/api/vault/projects/%FF", "/api/vault/projects/..", "/api/vault/%70rojects/id", "/api/skills-registry/groups/id/recover"} {
		response(t, h, vaultRequest("PUT", path, token, body), 404)
	}
	for _, method := range []string{"GET", "DELETE", "PATCH"} {
		response(t, h, vaultRequest(method, "/api/vault/projects/id", token, body), 404)
	}
	for _, mode := range []string{"token", "host", "origin", "fetch"} {
		r := vaultRequest("POST", "/api/vault/projects", token, body)
		switch mode {
		case "token":
			r.Header.Set(CapabilityHeader, "wrong")
		case "host":
			r.Host = "attacker.invalid"
		case "origin":
			r.Header.Set("Origin", "https://attacker.invalid")
		case "fetch":
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		response(t, h, r, 403)
	}
	readOnly, _ := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
	readToken := sessionTokenOf(readOnly)
	response(t, readOnly, vaultRequest("POST", "/api/vault/projects", readToken, body), 404)
	if len(s.calls) != 0 {
		t.Fatal("rejected call reached owner")
	}
}

func TestVaultEditFailuresRetainMultiFileFacts(t *testing.T) {
	h, token, s := applicationFixture(t)
	s.out = &ApplicationOutcome{Committed: true, Partial: true, CleanupFailed: true, Write: &ApplicationWriteState{IntentRecorded: true, AppliedFiles: 1}}
	s.err = &ApplicationError{Code: "SKILL_REGISTRY_OPERATION_FAILED"}
	result := response(t, h, vaultRequest("POST", "/api/skills-registry/groups/id/archive", token, `{"expected_revision":"x"}`), 500)
	state := result["operation_state"].(map[string]any)
	if state["partial"] != true || state["committed"] != true || state["cleanup_failed"] != true || state["write"].(map[string]any)["applied_files"] != float64(1) {
		t.Fatal("lost publication facts", state)
	}
	for code, status := range map[string]int{"SKILL_REGISTRY_REVISION_CONFLICT": 409, "CORE_SKILL_READ_ONLY": 409, "SKILL_GROUP_NOT_FOUND": 404, "SKILL_NAME_CONFLICT": 400, "SKILL_GROUP_ARCHIVED": 400} {
		s.err.Code = code
		result = response(t, h, vaultRequest("POST", "/api/skills-registry/groups/id/archive", token, `{"expected_revision":"x"}`), status)
		if result["error_code"] != code || strings.Contains(result["technical_message"].(string), "private") {
			t.Fatal("unsafe error mapping")
		}
	}
}

func TestCompetencyHTTPQueryAndReadOnlyContract(t *testing.T) {
	h, token, s := applicationFixture(t)
	for _, tc := range []struct{ query, op string }{
		{"", "active"}, {"?include_archived_competencies=TRUE", "archived"},
		{"?include_archived_competencies=1", "archived"}, {"?include_archived_competencies=%20true", "active"},
		{"?include_archived_competencies=&include_archived_competencies=true", "archived"},
		{"?include_archived_competencies=false&include_archived_competencies=true", "active"},
	} {
		response(t, h, request("GET", "/api/competency-explorer"+tc.query, token), 200)
		if s.calls[len(s.calls)-1] != "competencies."+tc.op {
			t.Fatal("query semantics")
		}
	}
	before := len(s.calls)
	response(t, h, appPut("/api/competency-explorer", token, `{}`), 404)
	if len(s.calls) != before {
		t.Fatal("read projection accepted mutation")
	}
	s.err = &ApplicationError{Code: "private-marker"}
	result := response(t, h, request("GET", "/api/competency-explorer", token), 503)
	if result["error_code"] != "COMPETENCY_PROJECTION_UNAVAILABLE" {
		t.Fatal("missing safe projection error")
	}
}
