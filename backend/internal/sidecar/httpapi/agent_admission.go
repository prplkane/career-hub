package httpapi

import (
	"crypto/subtle"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
)

// The agent token is a second per-start capability, separate from the UI
// token, for the career-hub-agent client (ADR SEC-250). It is never returned by
// any route: the composition root publishes it only to an owner-only file in
// the Data Home. It admits exactly the routes the agent client calls, so a
// leaked agent token cannot read or edit the rest of the API.
func (h *handler) configureAgent(random io.Reader) error {
	var seed [32]byte
	if _, err := io.ReadFull(random, seed[:]); err != nil {
		clear(seed[:])
		return &Error{Code: CodeConfiguration}
	}
	h.agent = base64.RawURLEncoding.EncodeToString(seed[:])
	clear(seed[:])
	return nil
}

// AgentToken returns the per-start agent token of an application handler built
// by this package. Only the trusted composition root calls it.
func AgentToken(h http.Handler) (string, bool) {
	typed, ok := h.(*handler)
	if !ok || typed.agent == "" {
		return "", false
	}
	return typed.agent, true
}

func (h *handler) isAgentToken(token string) bool {
	return h.agent != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.agent)) == 1
}

// agentRoute is the agent client's complete scope: the paths in
// internal/agentmaterials. Anything else, including vault, settings and email
// routes, is refused for the agent token. Job ids arrive escaped and must be a
// single non-empty decoded segment.
func agentRoute(method, path string) bool {
	switch {
	case method == http.MethodGet && (path == "/api/session" || path == "/api/registry"):
		// /api/registry is the read-only skills catalog the assess-job skill
		// checks before registering an exact JD technology.
		return true
	case method == http.MethodPost && (path == "/api/agent/analyze" || path == "/api/agent/prepare"):
		return true
	}
	rest, ok := strings.CutPrefix(path, "/api/jobs/")
	if !ok {
		return false
	}
	job, tail, found := strings.Cut(rest, "/")
	if !found || job == "" {
		return false
	}
	switch tail {
	case "agent-context", "application-texts", "application/record-submission", "vocabulary/exact":
		return method == http.MethodPost
	case "materials/resume", "materials/cover-letter":
		return method == http.MethodGet || method == http.MethodPost
	case "materials/resume/exports", "materials/cover-letter/exports":
		return method == http.MethodPost
	}
	return false
}

// UseSessionToken makes a handler built by this package accept the desktop
// host's window token (ADR SEC-250) instead of the one it generated. Only the
// trusted composition root calls it, before serving, with a token the start
// record parser has already validated. An empty token keeps the generated one.
func UseSessionToken(h http.Handler, token string) error {
	typed, ok := h.(*handler)
	if !ok {
		return &Error{Code: CodeConfiguration}
	}
	if token == "" {
		return nil
	}
	if token == typed.agent {
		return &Error{Code: CodeConfiguration}
	}
	typed.token = token
	return nil
}
