package emailconnection

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

type fakeSecrets struct {
	values           map[string][]byte
	reads            []string
	readError        bool
	deleteAfterError bool
	onRead           func()
}

func (s *fakeSecrets) Write(target string, value []byte) error {
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	s.values[target] = bytes.Clone(value)
	return nil
}

func (s *fakeSecrets) Read(target string) ([]byte, bool, error) {
	s.reads = append(s.reads, target)
	if s.onRead != nil {
		s.onRead()
	}
	if s.readError {
		return nil, false, errors.New("private fake secret-store details")
	}
	value, found := s.values[target]
	return bytes.Clone(value), found, nil
}

func (s *fakeSecrets) Delete(target string) (bool, error) {
	_, found := s.values[target]
	delete(s.values, target)
	if s.deleteAfterError {
		s.deleteAfterError = false
		return found, errors.New("private fake delete details")
	}
	return found, nil
}

func TestRecoveryRefusesUnownedAndInvalidCalls(t *testing.T) {
	backend := &fakeSecrets{}
	if result, problem := Recover(context.Background(), t.TempDir(), backend); result != (Result{}) || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unowned recovery admitted: %+v %v", result, problem)
	}
	if result, problem := Recover(nil, t.TempDir(), backend); result != (Result{}) || problem == nil || problem.Code != "EMAIL_CREDENTIAL_RECOVERY_INVALID" {
		t.Fatalf("nil context admitted: %+v %v", result, problem)
	}
	if result, problem := Recover(context.Background(), t.TempDir(), nil); result != (Result{}) || problem == nil || problem.Code != "EMAIL_CREDENTIAL_RECOVERY_INVALID" {
		t.Fatalf("nil backend admitted: %+v %v", result, problem)
	}
}
