package intakeread

// DigestStatus applies the source Resume Library's final management precedence.
// The owner must first select one strict-valid matching extraction run and
// derive its workflow state. Filename support and processed time are separate
// fields supplied by that owner; this pure function reads no files or database.
func DigestStatus(hasRun bool, workflowStatus, runRegistryRevision, currentRegistryRevision, runExtractorVersion, currentExtractorVersion string) string {
	if !hasRun {
		return "not_digested"
	}
	if workflowStatus == "review_error" {
		return "error"
	}
	if runRegistryRevision != currentRegistryRevision || runExtractorVersion != currentExtractorVersion {
		return "outdated"
	}
	if workflowStatus == "saved_to_vault" {
		return "digested"
	}
	return "in_progress"
}
