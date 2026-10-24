//go:build !windows

package secretstore

// NewWindowsBackend refuses unsupported platforms; it never selects an alternate
// key store or plaintext fallback. A future macOS adapter is a separate contract.
func NewWindowsBackend() (Backend, error) {
	return nil, &BackendError{Code: CodeBackendUnavailable}
}
