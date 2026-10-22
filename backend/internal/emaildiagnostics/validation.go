package emaildiagnostics

import (
	"math"
	"regexp"
	"time"

	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/emailworker"
)

var timestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{6})?\+00:00$`)

func exactKeys(values map[string]any, names ...string) bool {
	if len(values) != len(names) {
		return false
	}
	for _, name := range names {
		if _, exists := values[name]; !exists {
			return false
		}
	}
	return true
}

func boundedNumber(value any, min, max float64) bool {
	number, ok := value.(float64)
	return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && number >= min && number <= max
}

func boundedInt(value any, min, max float64) bool {
	return boundedNumber(value, min, max) && math.Trunc(value.(float64)) == value.(float64)
}

func validCounts(raw any) bool {
	counts, ok := raw.(map[string]any)
	expected := diagnosticCounts(emailworker.Counts{})
	if !ok || len(counts) != len(expected) {
		return false
	}
	for name := range expected {
		if !boundedInt(counts[name], 0, emailworker.MaxMessagesPerSync) {
			return false
		}
	}
	return true
}

func validFailures(raw any) bool {
	counts, ok := raw.(map[string]any)
	if !ok || len(counts) > 64 {
		return false
	}
	for code, count := range counts {
		if !codePattern.MatchString(code) || !boundedInt(count, 1, emailworker.MaxMessagesPerSync) {
			return false
		}
	}
	return true
}

func validRecordDetails(event record) bool {
	if !timestampPattern.MatchString(event.Timestamp) {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, event.Timestamp); err != nil {
		return false
	}
	operation := ""
	switch {
	case event.Source == "gmail_sync" && event.Stage == "email_sync_run":
		operation = "sync"
	case event.Source == "gmail_connection" && event.Stage == "email_connection_run":
		operation = "save"
	default:
		return false
	}
	details := event.Details
	if details == nil || details["operation"] != operation {
		return false
	}
	if event.Sequence == 1 {
		return event.Event == "started" && event.Status == "running" && exactKeys(details, "operation")
	}
	if event.Event == "completed" && event.Status == "success" {
		if operation == "save" {
			return exactKeys(details, "operation", "revision", "credential_supplied", "total_duration_ms") &&
				boundedInt(details["revision"], 1, float64(emailcredentials.MaxRevision)) &&
				isBool(details["credential_supplied"]) && validDuration(details)
		}
	}
	if operation == "sync" {
		return validSyncTerminal(event)
	}
	return validConnectionTerminal(event)
}

func validDuration(details map[string]any) bool {
	return boundedNumber(details["total_duration_ms"], 0, 1e15)
}

func isBool(value any) bool { _, ok := value.(bool); return ok }

func connectionCodeAllowed(code string) bool {
	if codePattern.MatchString(code) {
		return true
	}
	switch code {
	case "DATA_HOME_CHANGED", "DATA_HOME_OWNERSHIP_REQUIRED", "DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_UNAVAILABLE":
		return true
	}
	return false
}

func validSyncTerminal(event record) bool {
	details := event.Details
	if event.Event == "completed" && (event.Status == "success" || event.Status == "success_with_warnings") {
		resultStatus, ok := details["result_status"].(string)
		if !ok || (resultStatus != "succeeded" && resultStatus != "partial" && resultStatus != "disabled" && resultStatus != "deferred") ||
			(resultStatus == "partial") != (event.Status == "success_with_warnings") {
			return false
		}
		if details["trigger"] != "manual" && details["trigger"] != "scheduled" ||
			!validCounts(details["counts"]) || !validFailures(details["failure_code_counts"]) ||
			!isBool(details["has_more"]) || !validDuration(details) {
			return false
		}
		if resultStatus != "partial" {
			return exactKeys(details, "operation", "trigger", "result_status", "counts", "failure_code_counts", "has_more", "total_duration_ms")
		}
		code, ok := details["error_code"].(string)
		if !ok || code != "EMAIL_SYNC_MESSAGE_RETRY" && code != "EMAIL_SYNC_MESSAGE_SKIPPED" ||
			!exactKeys(details, "operation", "trigger", "result_status", "counts", "failure_code_counts", "has_more", "total_duration_ms", "error_code", "summary", "suggestion") {
			return false
		}
		summary, suggestion := partialGuidance(code)
		return details["summary"] == summary && details["suggestion"] == suggestion
	}
	if event.Event != "failed" || event.Status != "error" ||
		!exactKeys(details, "operation", "error_code", "failed_stage", "total_duration_ms", "summary", "suggestion", "severity", "retryable") {
		return false
	}
	code, ok := details["error_code"].(string)
	if !ok || !codePattern.MatchString(code) || !validDuration(details) {
		return false
	}
	switch details["failed_stage"] {
	case "request", "mailbox", "local_persistence", "readback", "connection":
	default:
		return false
	}
	public, found := failureGuidance[code]
	if !found {
		public = failureGuidance["EMAIL_SYNC_FAILED"]
	}
	return details["summary"] == public.summary && details["suggestion"] == public.suggestion &&
		details["severity"] == public.severity && details["retryable"] == public.retryable
}

func validConnectionTerminal(event record) bool {
	details := event.Details
	if event.Event != "failed" || event.Status != "error" ||
		!exactKeys(details, "operation", "error_code", "failed_stage", "total_duration_ms", "commit_attempted", "committed", "recovery_required", "summary", "suggestion", "severity", "retryable") {
		return false
	}
	code, ok := details["error_code"].(string)
	if !ok || !connectionCodeAllowed(code) || !validDuration(details) ||
		!isBool(details["commit_attempted"]) || !isBool(details["committed"]) || !isBool(details["recovery_required"]) ||
		details["committed"] == true && details["commit_attempted"] != true {
		return false
	}
	switch details["failed_stage"] {
	case "request", "credential", "local_persistence", "readback", "connection":
	default:
		return false
	}
	return details["summary"] == "The email connection change could not be verified safely." &&
		details["suggestion"] == "Reload the connection and inspect its run ID before retrying." &&
		details["severity"] == "error" && details["retryable"] == false
}
