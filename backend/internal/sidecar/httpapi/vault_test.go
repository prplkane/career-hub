package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"careerhub.local/backend/internal/storage/documents/ledger"
	"careerhub.local/backend/internal/vault/vaultread"
)

var expectedVaultRoutes = []struct {
	path string
	view vaultread.View
}{
	{"/api/vault/profile", vaultread.ProfileView},
	{"/api/vault/eligibility", vaultread.EligibilityView},
	{"/api/vault/education", vaultread.EducationView},
	{"/api/vault/experiences", vaultread.ExperiencesView},
	{"/api/vault/projects", vaultread.ProjectsView},
	{"/api/vault/skills", vaultread.SkillsView},
	{"/api/vault/snapshot", vaultread.SnapshotView},
	{"/api/vault/setup", vaultread.SetupView},
}

func TestEveryVaultRouteAuthenticatesThenSelectsOneFreshRead(t *testing.T) {
	type contextKey struct{}
	for _, tc := range expectedVaultRoutes {
		t.Run(tc.path, func(t *testing.T) {
			calls := 0
			h, token := fixture(t, func() (map[string]any, error) {
				t.Fatal("Vault request consulted registry")
				return nil, nil
			})
			h.(*handler).vault = func(ctx context.Context, view vaultread.View) (json.RawMessage, error) {
				if view != tc.view || ctx.Value(contextKey{}) != "synthetic" {
					t.Fatal("fixed view or request context was lost")
				}
				calls++
				return json.RawMessage(fmt.Sprintf(`{"revision":%d}`, calls)), nil
			}
			for _, denial := range []struct {
				method, suffix, auth, header, value string
				status                              int
			}{
				{"GET", "", "", "", "", 403},
				{"GET", "", "wrong", "", "", 403},
				{"GET", "", token, "Origin", "https://outside.invalid", 403},
				{"OPTIONS", "", token, "", "", 403},
				{"POST", "", token, "", "", 404},
				{"PUT", "", token, "", "", 404},
				{"DELETE", "", token, "", "", 404},
				{"HEAD", "", token, "", "", 404},
				{"GET", "/extra", token, "", "", 404},
				{"GET", "?x=" + strings.Repeat("a", MaxRequestTargetBytes), token, "", "", 404},
			} {
				r := request(denial.method, tc.path+denial.suffix, denial.auth)
				if denial.header != "" {
					r.Header.Set(denial.header, denial.value)
				}
				response(t, h, r, denial.status)
			}
			r := request("GET", tc.path, token)
			r.Host = "outside.invalid"
			response(t, h, r, 403)
			r = request("GET", tc.path, token)
			r.Header.Add(CapabilityHeader, token)
			response(t, h, r, 403)
			if calls != 0 {
				t.Fatal("denial or unsupported method read private data")
			}
			for count := 1; count <= 2; count++ {
				r := request("GET", tc.path+"?view=../../private.yaml&path=profile&ignored=1", token)
				r = r.WithContext(context.WithValue(r.Context(), contextKey{}, "synthetic"))
				got := response(t, h, r, 200)
				if got["revision"] != float64(count) || calls != count {
					t.Fatal("route cached a response or selected a path from its query")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			got := response(t, h, request("GET", tc.path, token).WithContext(ctx), 500)
			if got["error_code"] != "VAULT_READ_CANCELED" || calls != 2 {
				t.Fatal("canceled Vault request reached loader")
			}
		})
	}
}

func TestUnknownVaultRoutesNeverSelectARead(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) { return nil, nil })
	h.(*handler).vault = func(context.Context, vaultread.View) (json.RawMessage, error) {
		t.Fatal("unknown path selected a private read")
		return nil, nil
	}
	for _, target := range []string{
		"/api/vault", "/api/vault/", "/api/vault/profile.yaml", "/api/vault/../../profile",
		"/api/vault/%70rofile", "/api/vault/Profiles", "/api/vault/demographics",
		"/api/vault/snapshot/", "/api/vault/skills-registry", "/api/vault/0",
	} {
		response(t, h, request("GET", target, token), 404)
	}
}

func TestAllVaultRoutesKeepSafeFailureCategoriesAndReleaseLock(t *testing.T) {
	for _, route := range expectedVaultRoutes {
		for _, tc := range []struct {
			code   string
			status int
			read   Vault
		}{
			{"VAULT_RECORD_VALIDATION_FAILED", 409, func(context.Context, vaultread.View) (json.RawMessage, error) {
				return nil, &vaultread.Error{Code: "VAULT_FILE_MISSING", Source: "private ID"}
			}},
			{"VAULT_PROJECTION_LIMIT", 500, func(context.Context, vaultread.View) (json.RawMessage, error) {
				return nil, &vaultread.Error{Code: "VAULT_PROJECTION_LIMIT"}
			}},
			{"VAULT_PROJECTION_UNSUPPORTED", 500, func(context.Context, vaultread.View) (json.RawMessage, error) {
				return nil, &vaultread.Error{Code: "VAULT_PROJECTION_UNSUPPORTED"}
			}},
			{"UNEXPECTED_ERROR", 500, func(context.Context, vaultread.View) (json.RawMessage, error) {
				return nil, errors.New("private native path")
			}},
			{"UNEXPECTED_ERROR", 500, func(context.Context, vaultread.View) (json.RawMessage, error) {
				panic("private panic text")
			}},
		} {
			h, token := fixture(t, func() (map[string]any, error) { return nil, nil })
			h.(*handler).vault = tc.read
			got := response(t, h, request("GET", route.path, token), tc.status)
			raw, _ := json.Marshal(got)
			if got["error_code"] != tc.code || len(got) != 7 || strings.Contains(string(raw), "private") || strings.Contains(string(raw), token) {
				t.Fatal("Vault error classification or privacy changed")
			}
			h.(*handler).vault = emptyProfile
			response(t, h, request("GET", route.path, token), 200)
		}
	}
}

func TestRegistryAndAllVaultCallbacksShareSerialization(t *testing.T) {
	var active, calls atomic.Int32
	var overlap atomic.Bool
	work := func() {
		if active.Add(1) != 1 {
			overlap.Store(true)
		}
		runtime.Gosched()
		calls.Add(1)
		active.Add(-1)
	}
	h, token := fixture(t, func() (map[string]any, error) {
		work()
		return map[string]any{}, nil
	})
	h.(*handler).vault = func(context.Context, vaultread.View) (json.RawMessage, error) {
		work()
		return json.RawMessage("{}"), nil
	}
	h.(*handler).documents = func(ctx context.Context, archived bool) (*ledger.Listing, error) {
		work()
		return emptyDocuments(ctx, archived)
	}
	paths := []string{"/api/skills-registry", "/api/documents"}
	for _, route := range expectedVaultRoutes {
		paths = append(paths, route.path)
	}
	var wg sync.WaitGroup
	for cycle := 0; cycle < 8; cycle++ {
		for _, path := range paths {
			wg.Go(func() { h.ServeHTTP(httptest.NewRecorder(), request("GET", path, token)) })
		}
	}
	wg.Wait()
	if overlap.Load() || active.Load() != 0 || calls.Load() != int32(8*len(paths)) {
		t.Fatal("callbacks overlapped, retained lock, or missed a read")
	}
}
