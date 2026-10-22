package legacydpapi

import (
	"fmt"
	"strings"
	"testing"
)

func requireError(t *testing.T, err error, code string) {
	t.Helper()
	typed, ok := err.(*Error)
	if !ok || typed.Code != code {
		t.Fatalf("expected fixed DPAPI category %s, got %v", code, err)
	}
}

func TestFixedErrorsNeverExposePrivateDetails(t *testing.T) {
	for code, want := range map[string]string{
		CodeUnavailable:          "Windows DPAPI is unavailable on this platform.",
		CodeInitializationFailed: "Windows DPAPI could not be initialized.",
		CodeInputInvalid:         "Windows DPAPI input is invalid.",
		CodeInputTooLarge:        "Windows DPAPI input exceeds the supported legacy size.",
		CodeDecryptFailed:        "Windows DPAPI decryption failed for this account.",
		CodeOutputInvalid:        "Windows DPAPI returned invalid legacy output.",
		CodeCleanupFailed:        "Windows DPAPI output cleanup failed.",
	} {
		err := fail(code)
		for _, message := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			if message != want {
				t.Fatal("fixed error message mismatch")
			}
		}
	}
	var absent *Error
	if absent.Error() != "Windows DPAPI operation failed." {
		t.Fatal("nil error receiver")
	}
	if strings.Contains(fmt.Sprintf("%#v", &Error{Code: "private marker"}), "private") {
		t.Fatal("unknown category disclosed raw content")
	}
}

func TestLegacyBoundaryConstants(t *testing.T) {
	if MaxCiphertextBytes != 20*1024*1024+64*1024 || MaxPlaintextBytes != 20*1024*1024 {
		t.Fatal("consumer safety limits changed")
	}
}
