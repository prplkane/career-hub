package maintenance

import (
	"context"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
)

func TestRestoreInspectionRoutesWithoutMutation(t *testing.T) {
	payload := map[string]any{"command": protocol.DataHomeRecoveryInspectCommand, "protocol_version": 1, "data_root": fixtureDataRoot, "resource_root": fixtureResourceRoot, "channel": "dev"}
	called := false
	out, code, status := executeRecoveryInspection(context.Background(), payload, func(ctx context.Context, start protocol.StartRecord) (datahome.RecoveryKind, error) {
		called = true
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no deadline")
		}
		return datahome.RecoveryRestore, nil
	})
	if !called || code != "" || status != 0 || len(out) != 3 || out["recovery_kind"] != "restore" {
		t.Fatal(out, code, status)
	}
}
