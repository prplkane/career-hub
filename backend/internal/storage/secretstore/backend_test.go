package secretstore

import (
	"fmt"
	"strings"
	"testing"
)

type testBackend struct{}

func (testBackend) Write(string, []byte) error        { return nil }
func (testBackend) Read(string) ([]byte, bool, error) { return nil, false, nil }
func (testBackend) Delete(string) (bool, error)       { return false, nil }

func TestNilBackend(t *testing.T) {
	var typedNil *testBackend
	if !IsNilBackend(nil) || !IsNilBackend(typedNil) || IsNilBackend(testBackend{}) || IsNilBackend(&testBackend{}) {
		t.Fatal("nil backend classification")
	}
}

func TestBackendErrorRedaction(t *testing.T) {
	for _, code := range []string{CodeBackendUnavailable, CodeWindowsFailure, CodeTargetInvalid, CodeBlobTooLarge, CodeInvalidResult, "private-code-marker"} {
		err := &BackendError{Code: code, Operation: "private-target-marker", Win32Code: 5}
		for _, message := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			if strings.Contains(message, "private-") {
				t.Fatal("backend error disclosed raw details")
			}
		}
	}
	for _, op := range []string{"read", "write", "delete"} {
		if (&BackendError{Code: CodeWindowsFailure, Operation: op, Win32Code: 5}).Error() != "Windows Credential Manager "+op+" failed (Win32 error 5)" {
			t.Fatal("source Win32 error prose changed")
		}
	}
	if (&BackendError{Code: CodeBackendUnavailable, Operation: "initialize"}).Error() != "Windows Credential Manager could not be initialized" {
		t.Fatal("initialization error mismatch")
	}
	var absent *BackendError
	if absent.Error() != "Windows Credential Manager operation failed" {
		t.Fatal("nil error receiver")
	}
}
