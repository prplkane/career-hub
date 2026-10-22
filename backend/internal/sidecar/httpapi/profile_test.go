package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"careerhub.local/backend/internal/vaultread"
)

type profileRead func(context.Context) (json.RawMessage, error)

func profileFixture(t *testing.T, read profileRead) (http.Handler, string) {
	t.Helper()
	h, token := fixture(t, func() (map[string]any, error) {
		t.Fatal("profile request read registry")
		return nil, nil
	})
	h.(*handler).vault = func(ctx context.Context, view vaultread.View) (json.RawMessage, error) {
		if view != vaultread.ProfileView {
			t.Fatal("profile route selected another view")
		}
		return read(ctx)
	}
	return h, token
}

func TestProfileConfigurationRequiredBeforeEntropy(t *testing.T) {
	h, err := newHandler(8766, "1.2.3", func() (map[string]any, error) { return nil, nil }, nil, emptyDocuments, failReader{t: t})
	if h != nil || err == nil || err.(*Error).Code != CodeConfiguration {
		t.Fatal("nil profile callback accepted")
	}
}

func TestProfileDenialAndRouteChecksBeforeRead(t *testing.T) {
	h, token := profileFixture(t, func(context.Context) (json.RawMessage, error) {
		t.Fatal("denied or unmatched request read private profile")
		return nil, nil
	})
	for _, tc := range []struct {
		name, method, target, auth, header, value string
		status                                    int
	}{
		{"missing_token", "GET", "/api/vault/profile", "", "", "", 403},
		{"wrong_token", "GET", "/api/vault/profile", "wrong", "", "", 403},
		{"cross_origin", "GET", "/api/vault/profile", token, "Origin", "https://outside.invalid", 403},
		{"preflight", "OPTIONS", "/api/vault/profile", token, "", "", 403},
		{"mutation", "POST", "/api/vault/profile", token, "", "", 404},
		{"different_route", "GET", "/api/vault/profile/extra", token, "", "", 404},
		{"escaped_route", "GET", "/api/vault/%70rofile", token, "", "", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.method, tc.target, tc.auth)
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			response(t, h, r, tc.status)
		})
	}
	r := request("GET", "/api/vault/profile", token)
	r.Host = "outside.invalid"
	response(t, h, r, 403)
	r = request("GET", "/api/vault/profile", token)
	r.Header.Add(CapabilityHeader, token)
	response(t, h, r, 403)
}

func TestProfileFreshPayloadAndRequestContext(t *testing.T) {
	type contextKey struct{}
	calls := 0
	h, token := profileFixture(t, func(ctx context.Context) (json.RawMessage, error) {
		if ctx.Value(contextKey{}) != "synthetic-context" {
			t.Fatal("request context was not passed to reader")
		}
		calls++
		return json.RawMessage(fmt.Sprintf(`{"record":{"name":"Synthetic"},"revision":"%d"}`, calls)), nil
	})
	for index := 1; index <= 2; index++ {
		r := request("GET", "/api/vault/profile?ignored=1", token)
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, "synthetic-context"))
		got := response(t, h, r, 200)
		if len(got) != 2 || got["revision"] != fmt.Sprint(index) {
			t.Fatal("profile read was cached or wrapper changed")
		}
	}
}

func TestProfileErrorStatusPrivacyAndResponseLimits(t *testing.T) {
	type profileCase struct {
		name   string
		read   profileRead
		code   string
		status int
	}
	cases := []profileCase{
		{"panic", func(context.Context) (json.RawMessage, error) { panic("private ID/path/secret") }, "UNEXPECTED_ERROR", 500},
		{"error", func(context.Context) (json.RawMessage, error) { return nil, errors.New("private ID/path/secret") }, "UNEXPECTED_ERROR", 500},
		{"nil", func(context.Context) (json.RawMessage, error) { return nil, nil }, "VAULT_RESPONSE_UNSUPPORTED", 500},
		{"array", func(context.Context) (json.RawMessage, error) { return []byte("[]"), nil }, "VAULT_RESPONSE_UNSUPPORTED", 500},
		{"invalid", func(context.Context) (json.RawMessage, error) { return []byte("{private"), nil }, "VAULT_RESPONSE_UNSUPPORTED", 500},
		{"oversized", func(context.Context) (json.RawMessage, error) {
			return bytes.Repeat([]byte("x"), MaxResponseBytes+1), nil
		}, "VAULT_RESPONSE_LIMIT", 500},
	}
	for _, code := range []string{"VAULT_FILE_MISSING", "VAULT_READ_FAILED", "VAULT_YAML_INVALID", "VAULT_ROOT_TYPE", "VAULT_RECORD_VALIDATION_FAILED"} {
		cases = append(cases, profileCase{code,
			func(context.Context) (json.RawMessage, error) {
				return nil, &vaultread.Error{Code: code, Source: "private", Issues: []vaultread.Issue{{Code: "secret"}}}
			},
			"VAULT_RECORD_VALIDATION_FAILED", 409})
	}
	for _, code := range []string{"VAULT_INPUT_UNSUPPORTED", "VAULT_INPUT_LIMIT", "VAULT_VALIDATION_UNSUPPORTED", "VAULT_VALIDATION_LIMIT", "VAULT_RESPONSE_UNSUPPORTED", "VAULT_RESPONSE_LIMIT", "VAULT_READ_CANCELED", "VAULT_PATH_INVALID", "VAULT_CLOSE_FAILED"} {
		cases = append(cases, profileCase{code,
			func(context.Context) (json.RawMessage, error) { return nil, &vaultread.Error{Code: code} }, code, 500})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, token := profileFixture(t, tc.read)
			got := response(t, h, request("GET", "/api/vault/profile", token), tc.status)
			raw, _ := json.Marshal(got)
			if got["error_code"] != tc.code || len(got) != 7 || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "secret") {
				t.Fatal("profile failure changed public envelope or exposed private detail")
			}
		})
	}
}

func TestProfileCanceledBeforeAndAfterCallback(t *testing.T) {
	for _, before := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		if before {
			cancel()
		}
		calls := 0
		h, token := profileFixture(t, func(context.Context) (json.RawMessage, error) {
			calls++
			cancel()
			return json.RawMessage(`{"record":{}}`), nil
		})
		r := request("GET", "/api/vault/profile", token).WithContext(ctx)
		got := response(t, h, r, 500)
		if got["error_code"] != "VAULT_READ_CANCELED" || before && calls != 0 || !before && calls != 1 {
			t.Fatal("canceled profile read was dispatched or returned private payload")
		}
		cancel()
	}
}

func TestProfileExactResponseBound(t *testing.T) {
	raw := []byte(`{"x":"` + strings.Repeat("x", MaxResponseBytes-8) + `"}`)
	if len(raw) != MaxResponseBytes {
		t.Fatal("bad synthetic boundary")
	}
	h, token := profileFixture(t, func(context.Context) (json.RawMessage, error) { return raw, nil })
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/api/vault/profile", token))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatal("exact response limit changed")
	}
}
