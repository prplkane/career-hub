// Package applicationwrite composes bounded application-ledger mutations.
// Public methods borrow native ownership and require an existing current-schema
// Data Home. Installed route integration remains a separate migration gate.
// Inputs never imply that a job was submitted.
package applicationwrite

import (
	"fmt"
	"io"

	"careerhub.local/backend/internal/applications"
)

// Error formatting exposes neither private input nor a native SQLite cause.
// Source metadata is only for explicit compatibility checks/controlled mapping.
type Error struct{ Stage, Code, SourceClass, SourceMessage string }

func (e *Error) Error() string              { return "Application ledger operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func invalid(message string) *Error {
	return &Error{Stage: "input", Code: "APPLICATION_LEDGER_INVALID",
		SourceClass: "ApplicationHistoryError", SourceMessage: message}
}

func applicationProblem(e *applications.Error, id string) *Error {
	if e == nil {
		return nil
	}
	if e.Code == "NOT_FOUND" {
		return &Error{Stage: "identity", Code: "APPLICATION_LEDGER_NOT_FOUND", SourceClass: "ApplicationNotFound", SourceMessage: "Application job is not registered: " + id}
	}
	if e.Code == "REVISION_CONFLICT" {
		return &Error{Stage: "revision", Code: "APPLICATION_LEDGER_REVISION_CONFLICT", SourceClass: "ApplicationRevisionConflict", SourceMessage: "Application changed after it was loaded: " + id}
	}
	return failed(e.Stage, e.Code)
}
