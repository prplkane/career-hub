package startup

import (
	"context"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
)

// RecoverApprovalForProcess is a one-shot maintenance operation. It claims the
// exact Home until process exit, admits only its approval journal, and starts
// neither an HTTP listener nor schema preparation. Replay still reconstructs
// every approved after-image before allowing the transaction to advance.
func RecoverApprovalForProcess(ctx context.Context, start protocol.StartRecord) (*applicationwrite.Outcome, error) {
	owner, err := openRecoveryOwnerForProcess(ctx, start, datahome.RecoveryApproval)
	if err != nil {
		return nil, err
	}
	out, problem := owner.RecoverIntakeApproval(ctx)
	if problem != nil {
		return out, problem
	}
	return out, nil
}
