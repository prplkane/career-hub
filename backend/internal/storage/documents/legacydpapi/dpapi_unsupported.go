//go:build !windows

package legacydpapi

// UnprotectLegacy refuses unsupported platforms before examining the input,
// matching the Python source. It never selects an alternate decryptor/store.
func UnprotectLegacy([]byte) ([]byte, error) { return nil, fail(CodeUnavailable) }
