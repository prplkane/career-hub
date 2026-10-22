package httpapi

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"careerhub.local/backend/internal/textcompat"
)

const extensionHelperSession = "/api/application-helper/session"

type extensionClients struct {
	origins         map[string]bool
	capture, helper string
}

func (*extensionClients) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted extension capabilities]")
}

// configureExtensions is construction-only. No runtime setter or origin supplied
// by a browser request can enable access. Host composition remains opt-in.
func (h *handler) configureExtensions(origins []string, random io.Reader) error {
	if h.extensions != nil || random == nil {
		return &Error{Code: CodeConfiguration}
	}
	configured, err := extensionOriginSet(origins)
	if err != nil {
		return err
	}
	c := &extensionClients{origins: configured}
	var seed [64]byte
	defer clear(seed[:])
	if _, err := io.ReadFull(random, seed[:]); err != nil {
		return &Error{Code: CodeToken}
	}
	c.capture = base64.RawURLEncoding.EncodeToString(seed[:32])
	c.helper = base64.RawURLEncoding.EncodeToString(seed[32:])
	if c.capture == c.helper || c.capture == h.token || c.helper == h.token {
		return &Error{Code: CodeToken}
	}
	h.extensions = c
	return nil
}

// ValidateExtensionOrigins lets the process reject invalid explicit arguments
// before opening a Data Home. It neither enables access nor generates tokens.
func ValidateExtensionOrigins(origins []string) error {
	_, err := extensionOriginSet(origins)
	return err
}

func extensionOriginSet(origins []string) (map[string]bool, error) {
	if len(origins) == 0 || len(origins) > 16 {
		return nil, &Error{Code: CodeConfiguration}
	}
	configured := make(map[string]bool)
	for _, origin := range origins {
		parts := strings.Split(origin, "://")
		if len(parts) != 2 || (parts[0] != "chrome-extension" && parts[0] != "moz-extension" && parts[0] != "safari-web-extension") {
			return nil, &Error{Code: CodeConfiguration}
		}
		host := strings.TrimSuffix(parts[1], "/")
		if len(host) == 0 || len(host) > 255 {
			return nil, &Error{Code: CodeConfiguration}
		}
		for _, r := range host {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
				return nil, &Error{Code: CodeConfiguration}
			}
		}
		configured[parts[0]+"://"+host] = true
	}
	return configured, nil
}

func extensionPurpose(path string) string {
	switch path {
	case "/api/discoveries/captures", "/api/application-sessions/claim", "/api/application-sessions/submission-signal", "/api/application-sessions/interrupt":
		return "capture"
	case "/api/application-helper/fields", "/api/application-helper/answers/save", "/api/application-helper/answers/delete":
		return "helper"
	}
	return ""
}

func (h *handler) extensionOrigin(origin string) bool {
	return h.extensions != nil && h.extensions.origins[origin]
}

func extensionFetchSite(site string) bool {
	switch site {
	case "", "none", "same-origin", "same-site", "cross-site":
		return true
	}
	return false
}

// Purpose paths are reserved even while disabled: browser capabilities never
// acquire helper fields or attended-session authority through route composition.
func (h *handler) authorizeExtension(w http.ResponseWriter, r *http.Request, path string) (handled, allowed bool) {
	origin, originSingle := oneHeader(r, "Origin")
	token, tokenSingle := oneHeader(r, CapabilityHeader)
	site, siteSingle := oneHeader(r, "Sec-Fetch-Site")
	origin = textcompat.Strip(origin)
	site = textcompat.Fold(textcompat.Strip(site))
	configured := h.extensionOrigin(origin)
	purpose := extensionPurpose(path)
	capture, helper := false, false
	if h.extensions != nil {
		capture = subtle.ConstantTimeCompare([]byte(token), []byte(h.extensions.capture)) == 1
		helper = subtle.ConstantTimeCompare([]byte(token), []byte(h.extensions.helper)) == 1
	}
	if !configured && !capture && !helper && purpose == "" && path != extensionHelperSession {
		return false, false
	}
	deny := func() (bool, bool) {
		sendError(w, http.StatusForbidden, "LOCAL_API_ACCESS_DENIED", "The request is outside the configured extension capability.")
		return true, false
	}
	if !originSingle || !tokenSingle || !siteSingle || !configured || !extensionFetchSite(site) ||
		r.URL == nil || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.URL.IsAbs() || r.URL.Host != "" || r.URL.Fragment != "" ||
		len(r.RequestURI) > MaxRequestTargetBytes || r.RequestURI != "" && r.RequestURI != path {
		return deny()
	}
	if purpose == "" && path != "/api/session" && path != extensionHelperSession && path != extensionPairPath {
		return deny()
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	if r.Method == http.MethodOptions {
		// Bootstrap now carries the pairing credential header (ADR SEC-250), so
		// it is no longer a simple POST and may be preflighted like the others.
		method, methodSingle := oneHeader(r, "Access-Control-Request-Method")
		headers, headersSingle := oneHeader(r, "Access-Control-Request-Headers")
		if !methodSingle || !headersSingle || strings.ToUpper(method) != "POST" {
			return deny()
		}
		for _, name := range strings.Split(headers, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "" && name != "content-type" && name != strings.ToLower(CapabilityHeader) && name != strings.ToLower(ExtensionCredentialHeader) {
				return deny()
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "content-type, x-career-hub-token, x-career-hub-extension-credential")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return true, false
	}
	if r.Method != http.MethodPost {
		return deny()
	}
	if path == extensionPairPath {
		return true, true // redeeming a code needs no credential yet
	}
	if path == "/api/session" || path == extensionHelperSession {
		// The Origin above is a header a local program can send; the paired
		// credential is what proves this is the owner's extension.
		if !h.pairing.credentialMatches(r) {
			sendError(w, http.StatusForbidden, "EXTENSION_PAIRING_REQUIRED", "Pair the browser extension in Career Hub Settings.")
			return true, false
		}
		return true, true
	}
	if purpose == "capture" && capture || purpose == "helper" && helper {
		return true, true
	}
	return deny()
}

func (h *handler) serveExtensionBootstrap(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost || (r.URL.Path != "/api/session" && r.URL.Path != extensionHelperSession) {
		return false
	}
	// authorizeExtension ran first; do not expose either token to browser clients.
	origin, single := oneHeader(r, "Origin")
	if !single || !h.extensionOrigin(textcompat.Strip(origin)) {
		return false
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		sendError(w, http.StatusBadRequest, "EXTENSION_SESSION_REQUEST_INVALID", "Session bootstrap requires an empty request.")
		return true
	}
	if r.URL.Path == extensionHelperSession {
		sendJSON(w, map[string]any{"token": h.extensions.helper, "client": "application_helper", "contract_version": 2})
	} else {
		sendJSON(w, map[string]any{"token": h.extensions.capture, "client": "extension"})
	}
	return true
}
