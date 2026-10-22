package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (h *handler) serveEmailUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	offset, limit, valid := emailUpdatesPage(r.URL.RawQuery)
	if !valid {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_APPLICATION_UPDATE_INVALID", "email_application_updates_read")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.EmailUpdates(ctx, offset, limit)
	})
}

func emailUpdatesPage(raw string) (int64, int, bool) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return 0, 0, false
	}
	for key, values := range query {
		if (key != "limit" && key != "cursor") || len(values) != 1 {
			return 0, 0, false
		}
	}
	limit := 20
	if values, present := query["limit"]; present {
		value, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 32)
		if err != nil || value < 1 || value > 100 {
			return 0, 0, false
		}
		limit = int(value)
	}
	cursor := "offset:0"
	if values, present := query["cursor"]; present {
		cursor = values[0]
	}
	if !strings.HasPrefix(cursor, "offset:") {
		return 0, 0, false
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(cursor, "offset:")), 10, 64)
	return offset, limit, err == nil && offset >= 0
}
