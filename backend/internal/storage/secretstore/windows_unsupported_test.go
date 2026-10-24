//go:build !windows

package secretstore

import "testing"

func TestWindowsBackendUnavailable(t *testing.T) {
	backend, err := NewWindowsBackend()
	typed, ok := err.(*BackendError)
	if backend != nil || !ok || typed.Code != CodeBackendUnavailable {
		t.Fatal("unsupported platform selected a backend")
	}
}
