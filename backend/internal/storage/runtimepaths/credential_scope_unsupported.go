//go:build !windows

package runtimepaths

func credentialScopeForClaim(*processClaim) (CredentialScope, *Error) {
	return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNSUPPORTED")
}
