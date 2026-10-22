package maintenance

import (
	"context"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
)

type inspectRecovery func(context.Context, protocol.StartRecord) (datahome.RecoveryKind, error)

func executeRecoveryInspection(ctx context.Context, payload map[string]any, inspect inspectRecovery) (map[string]any, string, int) {
	request, err := protocol.ParseRecoveryMaintenance(payload, protocol.PathOptions{})
	if err != nil {
		return nil, requestErrorCode(err), 3
	}
	if ctx == nil || inspect == nil {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	kind, err := inspect(ctx, protocol.StartRecord{DataRoot: request.DataRoot, ResourceRoot: request.ResourceRoot, Channel: request.Channel})
	if err != nil {
		return nil, recoveryErrorCode(err), 4
	}
	if ctx.Err() != nil || (kind != datahome.RecoveryNewJob && kind != datahome.RecoveryEmailCredential && kind != datahome.RecoveryRestore) {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	return map[string]any{"event": "data_home_recovery_inspected", "protocol_version": protocol.Version, "recovery_kind": string(kind)}, "", 0
}
