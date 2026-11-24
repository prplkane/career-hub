package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"careerhub.local/backend/internal/vault/vaultbundle"
)

type exportSpy struct {
	calls   int
	content []byte
	meta    map[string]any
	err     *ApplicationError
	bundle  []byte
	request *VaultSetupRequest
}

func vaultExportFixture(t *testing.T, withExport bool) (http.Handler, string, *exportSpy) {
	t.Helper()
	s := &appSpy{out: &ApplicationOutcome{Result: json.RawMessage(`{"saved":true}`)}}
	e := &exportSpy{content: []byte(`{"format":"career-hub-vault-bundle"}`)}
	sum := sha256.Sum256(e.content)
	e.meta = map[string]any{"file_name": "career-vault-export-2026-11-18.chvault", "size_bytes": len(e.content), "sha256": hex.EncodeToString(sum[:])}
	actions := s.actions()
	actions.VaultSetup = func(_ context.Context, request VaultSetupRequest) (*ApplicationOutcome, *ApplicationError) {
		copied := request
		e.request = &copied
		e.bundle = bytes.Clone(request.Bundle)
		return &ApplicationOutcome{Result: json.RawMessage(`{"contract_version":1}`)}, nil
	}
	if withExport {
		actions.VaultExport = func(context.Context) (*ApplicationOutcome, *ApplicationError) {
			e.calls++
			if e.err != nil {
				return &ApplicationOutcome{}, e.err
			}
			raw, _ := json.Marshal(e.meta)
			return &ApplicationOutcome{Result: raw, Content: bytes.Clone(e.content)}, nil
		}
	}
	h, err := NewApplication(port, "0.1.7-dev", func() (map[string]any, error) { return map[string]any{}, nil }, emptyProfile, emptyDocuments, (&materialSpy{}).actions(), actions)
	if err != nil {
		t.Fatal(err)
	}
	return h, sessionTokenOf(h), e
}

func exportPost(token, body string) *http.Request {
	return emailImportPost(VaultExportPath, token, body)
}

func TestVaultExportCapabilityIsOptional(t *testing.T) {
	for _, withExport := range []bool{false, true} {
		h, token, spy := vaultExportFixture(t, withExport)
		caps := response(t, h, request(http.MethodGet, "/api/session", token), 200)["capabilities"].(map[string]any)
		if _, advertised := caps["vault_export"]; advertised != withExport || withExport && caps["vault_export"] != true {
			t.Fatal("vault_export advertised incorrectly", caps)
		}
		if caps["vault_setup_import"] != true || caps["vault_setup_empty"] != true {
			t.Fatal("existing setup capabilities changed", caps)
		}
		if !withExport {
			response(t, h, exportPost(token, `{"contract_version":1}`), 404)
			if spy.calls != 0 {
				t.Fatal("missing owner called")
			}
		}
	}
}

func TestVaultExportRouteSendsVerifiedFile(t *testing.T) {
	h, token, spy := vaultExportFixture(t, true)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, exportPost(token, `{"contract_version":1}`))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), spy.content) {
		t.Fatal("export not sent", w.Code)
	}
	header := w.Header()
	if header.Get("Content-Type") != "application/octet-stream" || header.Get("Cache-Control") != "no-store" ||
		header.Get("Content-Disposition") != `attachment; filename="career-vault-export-2026-11-18.chvault"` || header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("export headers", header)
	}
	before := spy.calls
	for _, invalid := range []string{`null`, `{}`, `{"contract_version":2}`, `{"contract_version":"1"}`, `{"contract_version":1,"contract_version":1}`, `{"contract_version":1,"path":"x"}`} {
		response(t, h, exportPost(token, invalid), 400)
	}
	response(t, h, exportPost("wrong", `{"contract_version":1}`), 403)
	response(t, h, emailImportPost(VaultExportPath+"?", token, `{"contract_version":1}`), 404)
	response(t, h, emailImportPost(VaultExportPath+"?x=1", token, `{"contract_version":1}`), 404)
	response(t, h, request(http.MethodGet, VaultExportPath, token), 404)
	cross := exportPost(token, `{"contract_version":1}`)
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	response(t, h, cross, 403)
	large := exportPost(token, `{"contract_version":1}`)
	large.ContentLength = 1025
	response(t, h, large, 413)
	if spy.calls != before {
		t.Fatal("rejected export reached owner")
	}
}

func TestVaultExportVerifiesOwnerOutput(t *testing.T) {
	h, token, spy := vaultExportFixture(t, true)
	good := spy.meta
	for name, mutate := range map[string]func(){
		"size":      func() { spy.meta["size_bytes"] = len(spy.content) + 1 },
		"digest":    func() { spy.meta["sha256"] = strings.Repeat("0", 64) },
		"name":      func() { spy.meta["file_name"] = "../profile.yaml" },
		"empty":     func() { spy.content = nil; spy.meta["size_bytes"] = 0 },
		"over size": func() { spy.meta["size_bytes"] = vaultbundle.MaxBundleBytes + 1 },
	} {
		spy.meta = map[string]any{}
		for key, value := range good {
			spy.meta[key] = value
		}
		saved := spy.content
		mutate()
		got := response(t, h, exportPost(token, `{"contract_version":1}`), 500)
		if got["error_code"] != "LOCAL_RESPONSE_INVALID" {
			t.Fatal(name, got)
		}
		spy.content = saved
	}
}

func TestVaultExportErrorCodes(t *testing.T) {
	h, token, spy := vaultExportFixture(t, true)
	for code, status := range map[string]int{
		"VAULT_EXPORT_NOT_CONFIGURED": 409, "VAULT_EXPORT_CHANGED": 409, "VAULT_EXPORT_INVALID": 422, "VAULT_EXPORT_SKILL_UNKNOWN": 422,
		"VAULT_EXPORT_TOO_LARGE": 413, "VAULT_EXPORT_UNAVAILABLE": 503, "VAULT_EXPORT_CANCELED": 408,
	} {
		spy.err = &ApplicationError{Code: code, Category: "vault_export"}
		got := response(t, h, exportPost(token, `{"contract_version":1}`), status)
		if got["error_code"] != code {
			t.Fatal("export code lost", code, got)
		}
	}
	spy.err = &ApplicationError{Code: "UNKNOWN_PRIVATE_DETAIL", Category: "vault_export"}
	if got := response(t, h, exportPost(token, `{"contract_version":1}`), 500); got["error_code"] != "LOCAL_OPERATION_FAILED" {
		t.Fatal("unknown code exposed", got)
	}
}

func bundleSetupBody(bundle []byte, apply bool) string {
	fields := map[string]any{"contract_version": 3, "bundle": base64.StdEncoding.EncodeToString(bundle)}
	if apply {
		fields["confirmed_plan"] = "sha256:" + strings.Repeat("c", 64)
	}
	raw, _ := json.Marshal(fields)
	return string(raw)
}

func TestVaultSetupExportFileEnvelope(t *testing.T) {
	h, token, spy := vaultExportFixture(t, true)
	bundle := []byte(`{"opaque":"bundle bytes"}`)
	for _, apply := range []bool{false, true} {
		path := "/api/vault/setup/preview"
		if apply {
			path = "/api/vault/setup/import"
		}
		body := bundleSetupBody(bundle, apply)
		spy.request = nil
		response(t, h, emailImportPost(path, token, body), 200)
		if spy.request == nil || !bytes.Equal(spy.bundle, bundle) || len(spy.request.Files) != 0 || spy.request.NewProfileName != nil || spy.request.Apply != apply {
			t.Fatal("typed export-file request lost")
		}
		if apply && spy.request.ConfirmedPlan != "sha256:"+strings.Repeat("c", 64) {
			t.Fatal("confirmation lost")
		}
		encoded := base64.StdEncoding.EncodeToString(bundle)
		spy.request = nil
		for _, invalid := range []string{
			strings.Replace(body, `"bundle":"`+encoded+`"`, `"bundle":null`, 1),
			strings.Replace(body, `"bundle":"`+encoded+`"`, `"bundle":""`, 1),
			strings.Replace(body, `"bundle":"`+encoded+`"`, `"bundle":{}`, 1),
			strings.Replace(body, `"bundle":"`+encoded+`"`, `"bundle":"not base64!"`, 1),
			strings.Replace(body, `"bundle":"`+encoded+`"`, `"bundle":"`+encoded[:8]+`\n`+encoded[8:]+`"`, 1),
			strings.Replace(body, `"bundle":"`+encoded+`"`, `"bundle":"`+encoded+`","bundle":"`+encoded+`"`, 1),
			strings.Replace(body, `"contract_version":3`, `"contract_version":4`, 1),
			strings.Replace(body, `"contract_version":3`, `"contract_version":1`, 1),
			strings.TrimSuffix(body, "}") + `,"files":{}}`,
			strings.TrimSuffix(body, "}") + `,"profile_name":"Synthetic"}`,
		} {
			response(t, h, emailImportPost(path, token, invalid), 400)
		}
		response(t, h, emailImportPost(path, "wrong", body), 403)
		response(t, h, emailImportPost(path+"?", token, body), 404)
		if spy.request != nil {
			t.Fatal("rejected export-file setup reached owner")
		}
	}
	// Preview without confirmed_plan on the apply route is refused.
	response(t, h, emailImportPost("/api/vault/setup/import", token, bundleSetupBody(bundle, false)), 400)
}

func TestVaultSetupExportFileSizeLimit(t *testing.T) {
	h, token, spy := vaultExportFixture(t, true)
	// The largest bundle import accepts must fit the 24 MiB request limit.
	largest := bytes.Repeat([]byte("z"), vaultbundle.MaxBundleBytes)
	body := bundleSetupBody(largest, true)
	if len(body) > 24<<20 {
		t.Fatal("largest bundle does not fit the request limit", len(body))
	}
	response(t, h, emailImportPost("/api/vault/setup/import", token, body), 200)
	if len(spy.bundle) != vaultbundle.MaxBundleBytes {
		t.Fatal("largest bundle truncated")
	}
	spy.request = nil
	response(t, h, emailImportPost("/api/vault/setup/preview", token, bundleSetupBody(append(largest, 'z'), false)), 400)
	if spy.request != nil {
		t.Fatal("oversized bundle reached owner")
	}
}

func TestVaultSetupExportFileErrorCodes(t *testing.T) {
	h, token, spy := applicationFixture(t)
	body := bundleSetupBody([]byte("x"), false)
	for _, code := range []string{"VAULT_SETUP_SKILL_UNKNOWN", "VAULT_SETUP_CATALOG_INVALID", "VAULT_SETUP_ANSWERS_INVALID",
		"VAULT_BUNDLE_INVALID", "VAULT_BUNDLE_DAMAGED", "VAULT_BUNDLE_VERSION_UNSUPPORTED", "VAULT_BUNDLE_PROTECTION_UNSUPPORTED", "VAULT_BUNDLE_LIMIT"} {
		spy.err = &ApplicationError{Code: code, Category: "vault_setup"}
		if got := response(t, h, emailImportPost("/api/vault/setup/preview", token, body), 422); got["error_code"] != code {
			t.Fatal("bundle code lost", code, got)
		}
	}
	spy.err = &ApplicationError{Code: "VAULT_SETUP_FILE_CONFLICT", Category: "vault_setup"}
	response(t, h, emailImportPost("/api/vault/setup/preview", token, body), 409)
}
