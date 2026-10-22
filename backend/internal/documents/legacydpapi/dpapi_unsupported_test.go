//go:build !windows

package legacydpapi

import "testing"

func TestUnsupportedPlatformPrecedesInputValidation(t *testing.T) {
	for _, content := range [][]byte{nil, {}, []byte("synthetic"), make([]byte, MaxCiphertextBytes+1)} {
		value, err := UnprotectLegacy(content)
		requireError(t, err, CodeUnavailable)
		if value != nil {
			t.Fatal("unsupported platform returned bytes")
		}
	}
}
