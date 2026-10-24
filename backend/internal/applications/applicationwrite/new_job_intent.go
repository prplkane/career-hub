package applicationwrite

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/storage/datahome"
)

// This private codec fixes the recovery identity for one new raw JD/card pair.
// It contains no JD text and is not connected to a writer, route or startup
// recovery. A future owner must record it durably before either file write.
const newJobIntentLimit = 4 << 10

type newJobIntent struct {
	Version            int           `json:"version"`
	Home               datahome.Info `json:"home"`
	OperationID        string        `json:"operation_id"`
	InitialStatus      string        `json:"initial_status"`
	JobID              string        `json:"job_id"`
	RawPath            string        `json:"raw_path"`
	CardPath           string        `json:"card_path"`
	RawSize            int           `json:"raw_size"`
	RawSHA256          string        `json:"raw_sha256"`
	CardSize           int           `json:"card_size"`
	CardSHA256         string        `json:"card_sha256"`
	ContentFingerprint string        `json:"content_fingerprint"`
}

func validNewJobIntent(p newJobIntent) bool {
	if p.Version != 1 || !validIntakeHome(p.Home) || !intakeID(p.OperationID) ||
		(p.InitialStatus != "captured" && p.InitialStatus != "applied") ||
		!validNewJobID(p.JobID) || p.RawSize < 1 || p.RawSize > jobdigest.MaxSourceBytes ||
		p.CardSize < 1 || p.CardSize > jobcard.MaxInputBytes || !validDigest(p.RawSHA256) ||
		!validDigest(p.CardSHA256) || !validDigest(p.ContentFingerprint) ||
		p.CardPath != "jobs/normalized/"+p.JobID+".yaml" {
		return false
	}
	return p.RawPath == "jobs/incoming/"+p.JobID+".md" ||
		p.RawPath == "jobs/incoming/"+p.JobID+".txt"
}

func validNewJobID(id string) bool {
	if !jobcard.ValidID(id) || len(id) > 200 || !utf8.ValidString(id) ||
		strings.HasPrefix(id, "_") || strings.HasSuffix(id, "_") {
		return false
	}
	for _, r := range id {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' {
			return false
		}
	}
	switch strings.ToUpper(id) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return false
	}
	return true
}

func encodeNewJobIntent(p newJobIntent) ([]byte, *Error) {
	if !validNewJobIntent(p) {
		return nil, failed("intent", "JOB_INTAKE_INTENT_INVALID")
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, failed("intent", "JOB_INTAKE_INTENT_INVALID")
	}
	raw, err := json.Marshal(discoveryEnvelope{Payload: payload, SHA256: digestBytes(payload)})
	if err != nil || len(raw) > newJobIntentLimit {
		return nil, failed("intent", "JOB_INTAKE_INTENT_LIMIT")
	}
	return raw, nil
}

func decodeNewJobIntent(raw []byte, home datahome.Info) (newJobIntent, *Error) {
	var envelope discoveryEnvelope
	var p newJobIntent
	if len(raw) == 0 || len(raw) > newJobIntentLimit || !canonicalIntentJSON(raw, &envelope) ||
		envelope.SHA256 != digestBytes(envelope.Payload) || !canonicalIntentJSON(envelope.Payload, &p) || !validNewJobIntent(p) {
		return newJobIntent{}, failed("intent", "JOB_INTAKE_INTENT_INVALID")
	}
	if p.Home != home {
		return newJobIntent{}, failed("intent", "JOB_INTAKE_INTENT_HOME_CHANGED")
	}
	return p, nil
}

// This classification is only an observation under a validated intent. A
// digest-matched file is not proof that this operation created it; none/partial
// are not permission to replay, delete or overwrite files after an uncertain
// native call. A card without its raw source is always a conflict.
type newJobFileState string

const (
	newJobFilesNone     newJobFileState = "none"
	newJobFilesRawOnly  newJobFileState = "raw_only"
	newJobFilesComplete newJobFileState = "complete"
	newJobFilesConflict newJobFileState = "conflict"
)

type newJobObservedFile struct {
	Present bool
	Bytes   []byte
}

func classifyNewJobFiles(p newJobIntent, raw, card newJobObservedFile) newJobFileState {
	if !validNewJobIntent(p) || (!raw.Present && len(raw.Bytes) != 0) || (!card.Present && len(card.Bytes) != 0) ||
		(raw.Present && (len(raw.Bytes) != p.RawSize || digestBytes(raw.Bytes) != p.RawSHA256)) ||
		(card.Present && (len(card.Bytes) != p.CardSize || digestBytes(card.Bytes) != p.CardSHA256)) ||
		(card.Present && !raw.Present) {
		return newJobFilesConflict
	}
	if card.Present {
		return newJobFilesComplete
	}
	if raw.Present {
		return newJobFilesRawOnly
	}
	return newJobFilesNone
}
