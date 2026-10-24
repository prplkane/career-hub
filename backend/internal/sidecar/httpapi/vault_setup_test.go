package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"careerhub.local/backend/internal/vault/vaultread"
)

func vaultSetupBody(apply bool) string {
	files := map[string]string{}
	for _, path := range vaultread.SetupPaths() {
		files[strings.TrimPrefix(path, "career_vault/")] = "YQ=="
	}
	fields := map[string]any{"contract_version": 1, "files": files}
	if apply {
		fields["confirmed_plan"] = "sha256:" + strings.Repeat("a", 64)
	}
	raw, _ := json.Marshal(fields)
	return string(raw)
}

func TestVaultSetupImportRoutesAreStrictAndAuthenticated(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, apply := range []bool{false, true} {
		path := "/api/vault/setup/preview"
		if apply {
			path = "/api/vault/setup/import"
		}
		body := vaultSetupBody(apply)
		response(t, h, emailImportPost(path, token, body), 200)
		if spy.fields["apply"] != apply || spy.fields["files"] != 8 {
			t.Fatal("lost typed setup request")
		}
		before := len(spy.calls)
		for _, invalid := range []string{
			`null`, `{}`, strings.Replace(body, `"contract_version":1`, `"contract_version":2`, 1),
			strings.Replace(body, `"profile.yaml":"YQ=="`, `"profile.yaml":"YQ==","profile.yaml":"Yg=="`, 1),
			strings.Replace(body, `profile.yaml`, `../profile.yaml`, 1),
			strings.Replace(body, `"profile.yaml":"YQ=="`, `"profile.yaml":null`, 1),
			strings.Replace(body, `"profile.yaml":"YQ=="`, `"profile.yaml":"not-base64"`, 1),
			strings.Replace(body, `"files":{`, `"files":{"unexpected.yaml":"YQ==",`, 1),
			strings.TrimSuffix(body, "}") + `,"extra":true}`,
		} {
			response(t, h, emailImportPost(path, token, invalid), 400)
		}
		response(t, h, emailImportPost(path, "wrong", body), 403)
		response(t, h, emailImportPost(path+"?", token, body), 404)
		response(t, h, emailImportPost(strings.Replace(path, "setup", "%73etup", 1), token, body), 404)
		response(t, h, request(http.MethodGet, path, token), 404)
		r := emailImportPost(path, token, body)
		r.ContentLength = (24 << 20) + 1
		response(t, h, r, 413)
		if len(spy.calls) != before {
			t.Fatal("rejected setup reached owner")
		}
	}
	response(t, h, emailImportPost("/api/vault/setup/import", token, vaultSetupBody(false)), 400)
}

func TestVaultSetupImportPreservesPartialWrites(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Committed: true, CommitUnknown: true, Partial: true, RevisionRefreshRequired: true}
	spy.err = &ApplicationError{Code: "VAULT_SETUP_PUBLICATION_UNVERIFIED", Category: "vault_setup"}
	got := response(t, h, emailImportPost("/api/vault/setup/import", token, vaultSetupBody(true)), 503)
	state := got["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != true || state["partial"] != true {
		t.Fatal("partial import hidden", got)
	}
	spy.out = &ApplicationOutcome{}
	spy.err = &ApplicationError{Code: "VAULT_SETUP_FILE_CONFLICT", Category: "vault_setup"}
	response(t, h, emailImportPost("/api/vault/setup/preview", token, vaultSetupBody(false)), 409)
	for _, code := range []string{"VAULT_YAML_INVALID", "VAULT_INPUT_UNSUPPORTED", "VAULT_INPUT_LIMIT", "VAULT_ROOT_TYPE", "VAULT_VALIDATION_UNSUPPORTED", "VAULT_VALIDATION_LIMIT", "VAULT_RECORD_VALIDATION_FAILED"} {
		spy.err = &ApplicationError{Code: code, Category: "vault_setup"}
		got := response(t, h, emailImportPost("/api/vault/setup/preview", token, vaultSetupBody(false)), 422)
		if got["error_code"] != code {
			t.Fatal("input validation code lost", got)
		}
	}
	spy.err = &ApplicationError{Code: "VAULT_READ_CANCELED", Category: "vault_setup"}
	response(t, h, emailImportPost("/api/vault/setup/preview", token, vaultSetupBody(false)), 408)
	for _, err := range []*ApplicationError{
		{Code: "VAULT_YAML_INVALID", Category: "other"},
		{Code: "UNKNOWN_PRIVATE_DETAIL", Category: "vault_setup"},
	} {
		spy.err = err
		got := response(t, h, emailImportPost("/api/vault/setup/preview", token, vaultSetupBody(false)), 500)
		if got["error_code"] != "LOCAL_OPERATION_FAILED" {
			t.Fatal("unexpected error code exposed", got)
		}
	}
}

func TestVaultSetupNewProfileClosedRequest(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, apply := range []bool{false, true} {
		path := "/api/vault/setup/preview"
		fields := map[string]any{"contract_version": 2, "profile_name": "Synthetic Ω"}
		if apply {
			path = "/api/vault/setup/import"
			fields["confirmed_plan"] = "sha256:" + strings.Repeat("b", 64)
		}
		raw, _ := json.Marshal(fields)
		body := string(raw)
		response(t, h, emailImportPost(path, token, body), 200)
		if spy.fields["apply"] != apply || spy.fields["files"] != 0 || spy.fields["profile_name"] != "Synthetic Ω" {
			t.Fatal("typed empty setup lost")
		}
		before := len(spy.calls)
		for _, invalid := range []string{
			strings.Replace(body, `"profile_name":"Synthetic Ω"`, `"profile_name":null`, 1),
			strings.Replace(body, `"profile_name":"Synthetic Ω"`, `"profile_name":false`, 1),
			strings.Replace(body, `"profile_name":"Synthetic Ω"`, `"profile_name":"first","profile_name":"second"`, 1),
			strings.Replace(body, `"profile_name":"Synthetic Ω"`, `"profile_name":"`+strings.Repeat("a", 4097)+`"`, 1),
			strings.Replace(body, `"contract_version":2`, `"contract_version":1`, 1),
			strings.Replace(body, `"contract_version":2`, `"contract_version":3`, 1),
			strings.TrimSuffix(body, "}") + `,"files":{}}`,
			strings.TrimSuffix(body, "}") + `,"requires_future_sponsorship":false}`,
		} {
			response(t, h, emailImportPost(path, token, invalid), 400)
		}
		response(t, h, emailImportPost(path, "wrong", body), 403)
		response(t, h, emailImportPost(path+"?", token, body), 404)
		if len(spy.calls) != before {
			t.Fatal("invalid new setup reached owner")
		}
	}
	spy.err = &ApplicationError{Code: "VAULT_SETUP_NAME_INVALID", Category: "vault_setup"}
	response(t, h, emailImportPost("/api/vault/setup/preview", token, `{"contract_version":2,"profile_name":" "}`), 400)
	spy.err = &ApplicationError{Code: "VAULT_SCHEMA_VERSION_UNSUPPORTED", Category: "vault_setup"}
	response(t, h, emailImportPost("/api/vault/setup/preview", token, vaultSetupBody(false)), 422)
}
