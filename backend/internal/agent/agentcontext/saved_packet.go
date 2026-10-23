package agentcontext

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationtexts"
	"careerhub.local/backend/internal/jobs/evaluationinput"
)

// SavedPacketRequest is internal host intent, not an HTTP DTO. A write owner
// supplies prepare's receipt only after successful write-owner cleanup. Binding
// checks prevent combining that receipt with a different card/application read;
// packet construction cannot certify publication itself.
type SavedPacketRequest struct {
	JobID, Purpose, Operation, RegistryMode string
	Profile, Channel                        string
	Task                                    *TaskRequest
	WriteReceipt                            json.RawMessage
	Preparation                             *PreparationBinding
}

// PreparationBinding is internal host input, never an HTTP/client field. Hashes
// identify the exact saved card bytes and JSON application summary observed by
// the write owner. The read still needs its own transaction and lifetime checks.
type PreparationBinding struct {
	CardSHA256, ApplicationSHA256 string
}

func (SavedPacketRequest) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted saved packet request]")
}

func ValidateSavedPacketRequest(r SavedPacketRequest) *Error {
	if r.JobID == "" || r.JobID == "." || r.JobID == ".." || len(r.JobID) > 256 || !utf8.ValidString(r.JobID) || strings.ContainsAny(r.JobID, "/\\:\x00") {
		return failure("JOB_ID_INVALID")
	}
	if _, e := TaskPayload(r.Purpose, r.Task); e != nil {
		return e
	}
	if r.Operation != "context" && r.Operation != "analyze" && r.Operation != "prepare" {
		return failure("PACKET_INTENT")
	}
	if r.RegistryMode != "off" && r.RegistryMode != "exact-auto" {
		return failure("PACKET_INTENT")
	}
	if len(r.Profile)+len(r.Channel) > 4096 || !utf8.ValidString(r.Profile) || !utf8.ValidString(r.Channel) {
		return failure("PACKET_INPUT")
	}
	if len(r.WriteReceipt) > 64<<10 {
		return failure("PACKET_LIMIT")
	}
	if r.Operation == "prepare" && len(r.WriteReceipt) == 0 {
		return failure("WRITE_RECEIPT_REQUIRED")
	}
	if r.Operation == "prepare" {
		if r.Preparation == nil || !preparationHash(r.Preparation.CardSHA256) || !preparationHash(r.Preparation.ApplicationSHA256) {
			return failure("PREPARATION_BINDING_REQUIRED")
		}
	} else if r.Preparation != nil {
		return failure("PACKET_INTENT")
	}
	if len(r.WriteReceipt) > 0 {
		v, e := decodeOrderedContextJSON(context.Background(), r.WriteReceipt)
		if e != nil || v.Kind != "mapping" {
			return failure("PACKET_INPUT")
		}
	}
	return nil
}

// PrepareSavedPacket assembles the complete saved packet in a caller-owned,
// current-schema SQL transaction. It does not acquire/open/commit/close anything.
// Sources must remain observed by the native owner through its final gate and
// synchronous cleanup. Digest reads are lazy and must join that same lifetime.
func PrepareSavedPacket(ctx context.Context, tx *sql.Tx, data, resources evaluationinput.Source, request SavedPacketRequest, today time.Time, digest ResumeDigestReader) ([]byte, *Error) {
	if e := contextStopped(ctx); e != nil {
		return nil, e
	}
	if e := ValidateSavedPacketRequest(request); e != nil {
		return nil, e
	}
	if tx == nil || data == nil || resources == nil || today.Year() < 1 || today.Year() > 9999 {
		return nil, failure("SOURCE_REQUIRED")
	}
	remaining := contextJSONLimit
	d := &observedEvidenceSource{ctx: ctx, source: data, files: map[string]evidenceFile{}, remaining: &remaining}
	r := &observedEvidenceSource{ctx: ctx, source: resources, files: map[string]evidenceFile{}, remaining: &remaining}
	if request.Preparation != nil {
		raw, present, problem := d.ReadFile("jobs/normalized/"+request.JobID+".yaml", 4<<20)
		if problem != nil || !present || !preparationMatches(raw, request.Preparation.CardSHA256) {
			return nil, failure("PREPARATION_CHANGED")
		}
	}
	evidence, e := readSavedEvidence(ctx, d, r, request.JobID, today)
	if e != nil {
		return nil, e
	}
	input := PacketInput{Job: evidence.Job, Assessment: evidence.Assessment, Sources: evidence.Sources,
		RegistryGroups: evidence.RegistryGroups, Purpose: request.Purpose, Operation: request.Operation,
		RegistryMode: request.RegistryMode, Profile: request.Profile, Channel: request.Channel,
		Persisted: true, Task: request.Task, WriteReceipt: request.WriteReceipt, ResumeInitialized: true}
	if len(input.WriteReceipt) == 0 {
		input.WriteReceipt = json.RawMessage(`{"result":"read_only","changed":false}`)
	}
	summaries, ae := applications.SummariesInTransaction(ctx, tx, []string{request.JobID})
	if ae != nil {
		return nil, failure("APPLICATION_READ_FAILED")
	}
	if summary, found := summaries[request.JobID]; found {
		var err error
		input.Application, err = json.Marshal(summary)
		if err != nil {
			return nil, failure("APPLICATION_ENCODING")
		}
	}
	if request.Preparation != nil && (len(input.Application) == 0 || !preparationMatches(input.Application, request.Preparation.ApplicationSHA256)) {
		return nil, failure("PREPARATION_CHANGED")
	}
	if e := packetResumeLibrary(ctx, tx, &input); e != nil {
		return nil, e
	}
	if request.Purpose == "application_text" {
		input.Workbench, e = readSavedWorkbench(ctx, d, request.JobID, evidence.Job)
		if e != nil {
			return nil, e
		}
	}
	packet, e := BuildPacket(ctx, input, digest)
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

func preparationHash(text string) bool {
	if len(text) != sha256.Size*2 {
		return false
	}
	raw, err := hex.DecodeString(text)
	return err == nil && len(raw) == sha256.Size && text == strings.ToLower(text)
}

func preparationMatches(raw []byte, expected string) bool {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]) == expected
}

func packetResumeLibrary(ctx context.Context, tx *sql.Tx, input *PacketInput) *Error {
	library, ae := applications.ResumeLibraryInTransaction(ctx, tx, applications.ResumeFilters{})
	if ae != nil {
		// Only known legacy value/size failures may yield a bounded warning.
		if ae.Code != "DATABASE_VALUE_UNSUPPORTED" && ae.Code != "RESULT_LIMIT" {
			return failure("RESUME_READ_FAILED")
		}
		warning := "Saved Resume Library data could not be read."
		input.ResumeLibraryFailure = &warning
	} else {
		var err error
		input.ResumeLibrary, err = json.Marshal(library)
		if err != nil {
			return failure("RESUME_ENCODING")
		}
	}
	input.ResumeInitialized = true
	return nil
}

func readSavedWorkbench(ctx context.Context, source evaluationinput.Source, jobID string, job []byte) ([]byte, *Error) {
	card, e := decodeOrderedContextJSON(ctx, job)
	if e != nil {
		return nil, e
	}
	fp := packetGet(card, "content_fingerprint", packetNull())
	var fingerprint *string
	if fp.Kind == "string" {
		fingerprint = &fp.Text
	} else if fp.Kind != "null" {
		return nil, failure("WORKBENCH_IDENTITY")
	}
	path := "outputs/application_texts/" + jobID + "/application_texts.yaml"
	if source.Check(path) != nil {
		return nil, failure("WORKBENCH_READ_FAILED")
	}
	raw, present, pe := source.ReadFile(path, applicationtexts.MaxStoreBytes)
	if pe != nil {
		return nil, failure("WORKBENCH_READ_FAILED")
	}
	if !present {
		return nil, nil
	}
	store, we := applicationtexts.ParseWorkbench(ctx, raw, jobID, fingerprint)
	if we != nil {
		return nil, failure(we.Code)
	}
	// Observations and arbitrary root metadata are validated/preserved by the
	// storage parser but never belong to the writing context. Do not serialize
	// unrelated YAML dates/binary metadata merely to discard them downstream.
	items := packetGet(store, "items", packetList())
	for i, item := range items.Items {
		items.Items[i] = packetPick(item, "id", "kind", "exact_prompt", "response", "limits", "evidence_refs", "manual_checks", "state")
	}
	return encodeOrderedContextJSON(ctx, packetObject(packetField("items", items)), false)
}
