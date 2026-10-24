package maintenance

import (
	"context"
	"errors"
	"time"

	"careerhub.local/backend/internal/email/emailconnection"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/startup"
)

type executeEmailRecovery func(context.Context, protocol.StartRecord) (emailconnection.Result, error)

func executeEmailRecoveryPayload(ctx context.Context, payload map[string]any, execute executeEmailRecovery) (map[string]any, string, int) {
	start, err := protocol.ParseEmailCredentialRecoveryMaintenance(payload, protocol.PathOptions{})
	if err != nil {
		return nil, requestErrorCode(err), 3
	}
	if ctx == nil || execute == nil {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := execute(ctx, start)
	code := ""
	if err != nil {
		code = emailRecoveryCode(err)
	}
	side, cleanup := "unknown", "none"
	switch out.DatabaseSide {
	case emailcredentials.BeforeCommit:
		side = "before"
	case emailcredentials.AfterCommit:
		side = "after"
	}
	switch out.Cleanup {
	case emailcredentials.CleanupNew:
		cleanup = "new"
	case emailcredentials.CleanupOld:
		cleanup = "old"
	case emailcredentials.CleanupNone:
	default:
		cleanup = "unknown"
	}
	if code == "" && (ctx.Err() != nil || side == "unknown" || cleanup == "unknown" || !out.IntentRemoved || !out.CleanupConfirmed) {
		code = "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED"
	}
	// Retain observed cleanup even on failure; never expose target/account/value,
	// infer database rollback, or turn a removed journal into a safe retry claim.
	record := map[string]any{"event": "email_credential_recovered", "protocol_version": protocol.Version,
		"success": code == "", "database_side": side, "cleanup": cleanup,
		"cleanup_attempted": out.CleanupAttempted, "cleanup_confirmed": out.CleanupConfirmed,
		"intent_removed": out.IntentRemoved, "error_code": code}
	if code != "" {
		return record, code, 4
	}
	return record, "", 0
}

func emailRecoveryCode(err error) string {
	var admission *startup.Error
	if errors.As(err, &admission) && allowedStartupCode(admission.Code) {
		return admission.Code
	}
	var problem *emailconnection.Error
	if errors.As(err, &problem) {
		switch problem.Code {
		case "EMAIL_CREDENTIAL_RECOVERY_CANCELED", "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED",
			"EMAIL_CREDENTIAL_RECOVERY_REQUIRED", "EMAIL_CREDENTIAL_HOME_CHANGED",
			"EMAIL_CREDENTIAL_INTENT_INVALID", "EMAIL_CREDENTIAL_INTENT_SCOPE_CHANGED",
			"EMAIL_CREDENTIAL_INTENT_HOME_CHANGED", "EMAIL_CREDENTIAL_INTENT_CHANGED",
			"EMAIL_CREDENTIAL_INTENT_UNAVAILABLE", "EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED",
			"EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE", "EMAIL_CREDENTIAL_SCOPE_CHANGED",
			"EMAIL_CREDENTIAL_PATH_UNAVAILABLE", "EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE",
			"EMAIL_CREDENTIAL_DATABASE_PATH_CHANGED", "EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED",
			"EMAIL_SECRET_STORE_FAILED":
			return problem.Code
		}
	}
	return "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED"
}
