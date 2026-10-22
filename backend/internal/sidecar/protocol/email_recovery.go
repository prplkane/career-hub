package protocol

const EmailCredentialRecoveryCommand = "email_credential_recover"

// This confirms settling an existing scoped intent, never opening a mailbox,
// supplying a new password or choosing a credential target.
func ParseEmailCredentialRecoveryMaintenance(payload map[string]any, paths PathOptions) (StartRecord, error) {
	return parseConfirmedRecoveryMaintenance(payload, paths, EmailCredentialRecoveryCommand)
}
