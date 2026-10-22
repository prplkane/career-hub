package httpapi

import (
	"net/http"
)

type staticFile struct{ Name, Type string }

// Deliberately exact first-party asset allowlist. No URL path is joined to a
// filesystem root. static_assets_test.go fails when a module imported by the
// frontend is missing from this list.
var staticFiles = func() map[string]staticFile {
	files := map[string]staticFile{
		"/":            {"index.html", "text/html; charset=utf-8"},
		"/favicon.svg": {"favicon.svg", "image/svg+xml"},
		"/styles.css":  {"styles.css", "text/css; charset=utf-8"},
	}
	for _, name := range []string{"theme.js", "locale.js", "app.js", "navigation.js", "ui/api.js", "ui/client_diagnostics.js", "ui/connectivity.js", "ui/dates.js", "ui/diagnostics_paging.js", "ui/primitives.js", "features/jobs.js", "features/applications.js", "features/resumes.js", "features/materials.js", "features/material_review.js", "features/evidence_items.js", "features/documents.js", "features/discoveries.js", "features/contacts.js", "features/desktop_updates.js", "features/desktop_backups.js", "features/application_helper.js", "features/help.js", "features/extension_pairing.js"} {
		files["/"+name] = staticFile{name, "text/javascript; charset=utf-8"}
	}
	files["/features/desktop_agent.js"] = staticFile{"features/desktop_agent.js", "text/javascript; charset=utf-8"}
	for _, name := range []string{"tokens.css", "foundation.css", "skills.css", "diagnostics-settings.css", "help.css", "resume-review.css", "jobs.css", "applications.css", "career-vault.css", "resume-planning.css", "materials.css", "components.css", "insights.css", "themes.css", "discovery.css", "application-updates.css", "contacts.css", "connectivity.css"} {
		files["/styles/"+name] = staticFile{"styles/" + name, "text/css; charset=utf-8"}
	}
	for _, name := range []string{"IBMPlexSans-Regular.woff2", "IBMPlexSans-SemiBold.woff2", "IBMPlexSans-Bold.woff2", "IBMPlexMono-Regular.woff2", "IBMPlexMono-Bold.woff2"} {
		files["/fonts/"+name] = staticFile{"fonts/" + name, "font/woff2"}
	}
	return files
}()

func (h *handler) serveStatic(w http.ResponseWriter, r *http.Request, file staticFile) {
	defer func() {
		if recover() != nil {
			sendError(w, 500, "STATIC_RESOURCE_UNAVAILABLE", "The application resource could not be read safely.")
		}
	}()
	raw, err := h.application.Static(r.Context(), file.Name)
	if err != nil || len(raw) > MaxResponseBytes || r.Context().Err() != nil {
		sendError(w, 500, "STATIC_RESOURCE_UNAVAILABLE", "The application resource could not be read safely.")
		return
	}
	sendTypedBytes(w, 200, raw, file.Type)
}
