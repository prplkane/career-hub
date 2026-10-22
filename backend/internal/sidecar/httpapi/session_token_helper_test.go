package httpapi

import "net/http"

// sessionTokenOf returns the handler's browser token. Since ADR SEC-250 no
// route discloses it; in production the desktop host supplies it at startup.
func sessionTokenOf(h http.Handler) string {
	return h.(*handler).token
}
