package agentcontext

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/evaluationinput"
)

// SourcePacketRequest is internal owner intent, not a client-selected Home.
// It cannot ask for writing context, a task, a persisted job or a write receipt.
type SourcePacketRequest struct {
	Source                         SourceRequest
	RegistryMode, Profile, Channel string
}

func ValidateSourcePacketRequest(r SourcePacketRequest) *Error {
	if e := ValidateSourceRequest(r.Source); e != nil {
		return e
	}
	if r.RegistryMode != "off" && r.RegistryMode != "exact-auto" || len(r.Profile)+len(r.Channel) > 4096 || !utf8.ValidString(r.Profile) || !utf8.ValidString(r.Channel) {
		return failure("PACKET_INTENT")
	}
	return nil
}

// PrepareSourcePacket shares the caller's coherent read transaction. No job,
// application row, draft or digest history is read or registered for this text.
func PrepareSourcePacket(ctx context.Context, tx *sql.Tx, data, resources evaluationinput.Source, request SourcePacketRequest, today time.Time) ([]byte, *Error) {
	if e := contextStopped(ctx); e != nil {
		return nil, e
	}
	if e := ValidateSourcePacketRequest(request); e != nil {
		return nil, e
	}
	if tx == nil || data == nil || resources == nil {
		return nil, failure("SOURCE_REQUIRED")
	}
	remaining := contextJSONLimit
	d := &observedEvidenceSource{ctx: ctx, source: data, files: map[string]evidenceFile{}, remaining: &remaining}
	r := &observedEvidenceSource{ctx: ctx, source: resources, files: map[string]evidenceFile{}, remaining: &remaining}
	evidence, e := readSourceEvidence(ctx, d, r, request.Source, today)
	if e != nil {
		return nil, e
	}
	input := PacketInput{Job: evidence.Job, Assessment: evidence.Assessment, Sources: evidence.Sources, RegistryGroups: evidence.RegistryGroups, Purpose: "assessment", Operation: "analyze", RegistryMode: request.RegistryMode, Profile: request.Profile, Channel: request.Channel, Persisted: false, WriteReceipt: json.RawMessage(`{"result":"read_only","changed":false}`)}
	if e := packetResumeLibrary(ctx, tx, &input); e != nil {
		return nil, e
	}
	packet, e := BuildPacket(ctx, input, nil)
	if e != nil {
		return nil, e
	}
	if e := d.recheck(); e != nil {
		return nil, e
	}
	if e := r.recheck(); e != nil {
		return nil, e
	}
	return packet, nil
}
