package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"careerhub.local/backend/internal/vaultread"
)

func TestVaultSetupUsesOnlyAuthorizedFixedReadView(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) {
		t.Fatal("setup read registry")
		return nil, nil
	})
	calls := 0
	h.(*handler).vault = func(ctx context.Context, view vaultread.View) (json.RawMessage, error) {
		calls++
		if view != vaultread.SetupView || ctx.Err() != nil {
			t.Fatal("wrong setup view or canceled call")
		}
		return []byte(`{"contract_version":1,"state":"not_configured","missing":["profile"]}`), nil
	}
	for _, tc := range []struct {
		method, path, credential string
		status                   int
	}{
		{"GET", "/api/vault/setup", "", 403},
		{"GET", "/api/vault/setup", "invalid", 403},
		{"POST", "/api/vault/setup", token, 404},
		{"PUT", "/api/vault/setup", token, 404},
		{"GET", "/api/vault/setup/", token, 404},
		{"GET", "/api/vault/%73etup", token, 404},
	} {
		response(t, h, request(tc.method, tc.path, tc.credential), tc.status)
	}
	r := request("GET", "/api/vault/setup", token)
	r.Header.Set("Origin", "https://outside.invalid")
	response(t, h, r, 403)
	if calls != 0 {
		t.Fatal("denied request reached private reader")
	}
	got := response(t, h, request("GET", "/api/vault/setup", token), 200)
	if calls != 1 || got["state"] != "not_configured" || len(got) != 3 {
		t.Fatal("setup read altered the envelope")
	}
	h.(*handler).vault = func(context.Context, vaultread.View) (json.RawMessage, error) {
		return nil, &vaultread.Error{Code: "VAULT_READ_FAILED", Source: "private"}
	}
	got = response(t, h, request("GET", "/api/vault/setup", token), 409)
	if got["error_code"] != "VAULT_RECORD_VALIDATION_FAILED" {
		t.Fatal("failed setup became success")
	}
}
