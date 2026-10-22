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

	"careerhub.local/backend/internal/documents/ledger"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func emptyDocuments(context.Context, bool) (*ledger.Listing, error) {
	listing := ledger.Metadata(nil, nil, true)
	return &listing, nil
}

func TestDocumentConfigurationBeforeEntropy(t *testing.T) {
	h, e := newHandler(8766, "1.2.3", func() (map[string]any, error) { return nil, nil }, emptyProfile, nil, failReader{t: t})
	if h != nil || e == nil || e.(*Error).Code != CodeConfiguration {
		t.Fatal("nil document callback accepted")
	}
}

func TestDocumentRouteAuthorizationAndFreshRead(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) { t.Fatal("document request consulted registry"); return nil, nil })
	type contextKey struct{}
	calls := 0
	h.(*handler).documents = func(ctx context.Context, archived bool) (*ledger.Listing, error) {
		if ctx.Value(contextKey{}) != "synthetic" || archived {
			t.Fatal("request context or archive selector lost")
		}
		calls++
		listing := ledger.Metadata(nil, []byte{byte(calls)}, archived)
		return &listing, nil
	}
	for _, bad := range []struct {
		method, target, auth string
		status               int
	}{
		{"GET", "/api/documents", "", 403},
		{"GET", "/api/documents", "wrong", 403},
		{"OPTIONS", "/api/documents", token, 403},
		{"POST", "/api/documents", token, 404},
		{"PATCH", "/api/documents", token, 404},
		{"DELETE", "/api/documents", token, 404},
		{"HEAD", "/api/documents", token, 404},
		{"GET", "/api/documents/", token, 404},
		{"GET", "/api/documents/doc_synthetic/file", token, 404},
		{"GET", "/api/documents/titles", token, 404},
		{"GET", "/api/%64ocuments", token, 404},
		{"GET", "/api/documents?x=" + strings.Repeat("x", MaxRequestTargetBytes), token, 404},
	} {
		response(t, h, request(bad.method, bad.target, bad.auth), bad.status)
	}
	for _, header := range []string{"Host", "Origin", CapabilityHeader} {
		r := request("GET", "/api/documents", token)
		if header == "Host" {
			r.Host = "outside.invalid"
		} else {
			r.Header.Add(header, "outside.invalid")
		}
		response(t, h, r, 403)
	}
	if calls != 0 {
		t.Fatal("denied route read documents")
	}
	var prior any
	for range 2 {
		r := request("GET", "/api/documents?include_archived=&include_archived=false&include_archived=true&path=../../private&key=other", token)
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, "synthetic"))
		got := response(t, h, r, 200)
		if len(got) != 2 || got["revision"] == prior || len(got["documents"].([]any)) != 0 {
			t.Fatal("document projection was cached or reshaped")
		}
		prior = got["revision"]
	}
	if calls != 2 {
		t.Fatal("wrong document read count")
	}
}

func TestDocumentSafeErrorsCancellationAndLockRelease(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) { return nil, nil })
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"DOCUMENT_STORAGE_INVALID", 400}, {"DOCUMENT_FILE_INVALID", 400}, {"DOCUMENT_MIGRATION_REQUIRED", 400},
		{"DOCUMENT_KEY_STATE_MISSING", 400}, {"DOCUMENT_KEY_UNAVAILABLE", 400}, {"DOCUMENT_ENCRYPTION_INVALID", 400},
		{"DOCUMENT_ENCRYPTION_UNAVAILABLE", 400}, {"DOCUMENT_ENCRYPTION_FAILED", 400}, {"DOCUMENT_LEDGER_INVALID", 400},
		{"DOCUMENT_STORAGE_UNSUPPORTED", 500}, {"DOCUMENT_STORAGE_LIMIT", 500}, {"DOCUMENT_READ_CANCELED", 500},
		{"DOCUMENT_LEDGER_INPUT_UNSUPPORTED", 500},
		{"DATA_HOME_IDENTITY_CHANGED", 500}, {"private-unknown-category", 500},
	} {
		h.(*handler).documents = func(context.Context, bool) (*ledger.Listing, error) {
			return &ledger.Listing{Revision: "private-result"}, fmt.Errorf("private wrapping: %w", &readonly.Error{Code: tc.code})
		}
		code := tc.code
		if strings.HasPrefix(code, "DATA_HOME") || strings.HasPrefix(code, "private") {
			code = "UNEXPECTED_ERROR"
		}
		got := response(t, h, request("GET", "/api/documents", token), tc.status)
		raw, _ := json.Marshal(got)
		if got["error_code"] != code || len(got) != 7 || bytes.Contains(raw, []byte("private")) || bytes.Contains(raw, []byte(token)) {
			t.Fatal("document error leaked values or changed shape")
		}
	}
	for _, callback := range []Documents{
		func(context.Context, bool) (*ledger.Listing, error) { return nil, errors.New("private native error") },
		func(context.Context, bool) (*ledger.Listing, error) { panic("private document panic") },
	} {
		h.(*handler).documents = callback
		got := response(t, h, request("GET", "/api/documents", token), 500)
		if got["error_code"] != "UNEXPECTED_ERROR" {
			t.Fatal("unknown failure not sanitized")
		}
		h.(*handler).documents = emptyDocuments
		response(t, h, request("GET", "/api/documents", token), 200)
	}
	for _, listing := range []*ledger.Listing{nil, {Documents: nil}} {
		h.(*handler).documents = func(context.Context, bool) (*ledger.Listing, error) { return listing, nil }
		got := response(t, h, request("GET", "/api/documents", token), 500)
		if got["error_code"] != "DOCUMENT_RESPONSE_UNSUPPORTED" {
			t.Fatal("nil listing accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.(*handler).documents = func(context.Context, bool) (*ledger.Listing, error) {
		t.Fatal("canceled read dispatched")
		return nil, nil
	}
	got := response(t, h, request("GET", "/api/documents", token).WithContext(ctx), 500)
	if got["error_code"] != "DOCUMENT_READ_CANCELED" {
		t.Fatal("pre-read cancel lost")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	h.(*handler).documents = func(ctx context.Context, b bool) (*ledger.Listing, error) { cancel(); return emptyDocuments(ctx, b) }
	got = response(t, h, request("GET", "/api/documents", token).WithContext(ctx), 500)
	if got["error_code"] != "DOCUMENT_READ_CANCELED" || got["documents"] != nil {
		t.Fatal("canceled result published")
	}
}

func TestDocumentQueryGoldenCases(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"", true}, {"include_archived=", true}, {"include_archived", true},
		{"include_archived=&include_archived=false", false},
		{"include_archived=false&include_archived=true", false},
		{"include_archived=true&include_archived=false", true},
		{"%69nclude_archived=%74rue", true}, {"Include_archived=false", true},
		{"include_archived=false;other=true", false}, {"include_archived=+true", false},
		{"include_archived=%", false}, {"include_archived=%FF", false},
		{"include_archived=true%00", false}, {"include_archived=TRUE", true},
	} {
		if documentArchiveQuery(tc.query) != tc.want {
			t.Fatalf("query mismatch: %q", tc.query)
		}
	}
}

func syntheticDocument() ledger.Document {
	return ledger.Document{ID: "doc_" + strings.Repeat("1", 32), Title: "Synthetic title", Category: "General", Filename: "synthetic.pdf",
		ContentType: "application/pdf", SizeBytes: 1, SHA256: strings.Repeat("a", 64), CreatedAt: "2026-09-08T00:00:00Z", UpdatedAt: "2026-09-08T00:00:00Z"}
}

func TestDocumentSourceSizedResponseDoesNotBroadenOtherRoutes(t *testing.T) {
	docs := make([]ledger.Document, 1300)
	for i := range docs {
		docs[i] = syntheticDocument()
		docs[i].ID = fmt.Sprintf("doc_%032x", i)
		docs[i].Notes = strings.Repeat("\u2028", 5000)
	}
	raw, e := json.Marshal(map[string]any{"version": 1, "documents": docs})
	if e != nil {
		t.Fatal("fixture encode failed")
	}
	// Valid source JSON may contain literal line separators. Exercise its
	// expansion in Go's encoder, not merely a large ignored-whitespace input.
	raw = bytes.ReplaceAll(raw, []byte(`\u2028`), []byte("\u2028"))
	if len(raw) >= ledger.MaxDocumentBytes {
		t.Fatal("fixture exceeded source limit")
	}
	raw = append(raw, bytes.Repeat([]byte(" "), ledger.MaxDocumentBytes-len(raw))...)
	parsed, problem := ledger.Parse(raw)
	if problem != nil {
		t.Fatal("source-sized valid ledger rejected")
	}
	h, token := fixture(t, func() (map[string]any, error) {
		return map[string]any{"value": strings.Repeat("x", MaxResponseBytes)}, nil
	})
	h.(*handler).documents = func(context.Context, bool) (*ledger.Listing, error) {
		value := ledger.Metadata(parsed, []byte("synthetic"), true)
		return &value, nil
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/api/documents", token))
	if w.Code != http.StatusOK || w.Body.Len() <= MaxResponseBytes || w.Body.Len() > MaxDocumentResponseBytes || !json.Valid(w.Body.Bytes()) {
		t.Fatal("valid source-sized response truncated or refused")
	}
	got := response(t, h, request("GET", "/api/skills-registry", token), 500)
	if got["error_code"] != "UNEXPECTED_ERROR" || MaxResponseBytes != 4*1024*1024 {
		t.Fatal("unrelated response cap broadened")
	}
}

func TestDocumentSerializedLimitAndHTMLEscaping(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) { return nil, nil })
	d := syntheticDocument()
	d.Notes = "<>&\u2028"
	h.(*handler).documents = func(context.Context, bool) (*ledger.Listing, error) {
		v := ledger.Metadata([]ledger.Document{d}, nil, true)
		return &v, nil
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/api/documents", token))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("<>&")) || !bytes.Contains(w.Body.Bytes(), []byte(`\u2028`)) {
		t.Fatal("document JSON escaping changed")
	}
	// Deliberately violate the trusted callback's input contract to exercise
	// output refusal; no bytes/headers from the oversized result may escape.
	d.Notes = strings.Repeat("x", MaxDocumentResponseBytes)
	got := response(t, h, request("GET", "/api/documents", token), 500)
	if got["error_code"] != "DOCUMENT_RESPONSE_LIMIT" || got["documents"] != nil {
		t.Fatal("oversized result published")
	}
}
