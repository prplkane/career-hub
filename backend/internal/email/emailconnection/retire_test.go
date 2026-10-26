package emailconnection

import (
	"context"
	"testing"

	"careerhub.local/backend/internal/storage/secretstore"
)

func TestRetireRejectsInvalidAndUnowned(t *testing.T) {
	for _, operation := range []struct {
		name string
		call func(context.Context, string, secretstore.Backend, int64) (RetireResult, *Error)
	}{
		{"disconnect", Disconnect}, {"forget", Forget},
	} {
		t.Run(operation.name, func(t *testing.T) {
			backend := &fakeSecrets{}
			result, problem := operation.call(context.Background(), t.TempDir(), backend, 0)
			if result != (RetireResult{}) || problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
				t.Fatalf("invalid revision admitted: %+v %v", result, problem)
			}
			result, problem = operation.call(context.Background(), t.TempDir(), backend, 1)
			if result != (RetireResult{}) || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
				t.Fatalf("unowned Home admitted: %+v %v", result, problem)
			}
			if len(backend.values) != 0 {
				t.Fatal("rejected retirement touched credentials")
			}
		})
	}
}
