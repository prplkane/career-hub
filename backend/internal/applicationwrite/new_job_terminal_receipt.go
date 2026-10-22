package applicationwrite

import "encoding/json"

const newJobTerminalReceiptLimit = 1024

// This fixed, content-minimized receipt is published only after the intended
// SQL phase is positively observed. It carries no JD, URL or profile text.
// Its exact bytes let a later owned recovery distinguish a terminal attempt
// from an otherwise matching but unattributed SQL row.
type newJobTerminalReceipt struct {
	Version            int    `json:"version"`
	OperationID        string `json:"operation_id"`
	JobID              string `json:"job_id"`
	InitialStatus      string `json:"initial_status"`
	RawSHA256          string `json:"raw_sha256"`
	CardSHA256         string `json:"card_sha256"`
	ContentFingerprint string `json:"content_fingerprint"`
}

func newJobTerminalReceiptFile(intent newJobIntent) (string, []byte, *Error) {
	if !validNewJobIntent(intent) {
		return "", nil, failed("receipt", "JOB_INTAKE_RECEIPT_INVALID")
	}
	value := newJobTerminalReceipt{Version: 1, OperationID: intent.OperationID,
		JobID: intent.JobID, InitialStatus: intent.InitialStatus,
		RawSHA256: intent.RawSHA256, CardSHA256: intent.CardSHA256,
		ContentFingerprint: intent.ContentFingerprint}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > newJobTerminalReceiptLimit {
		return "", nil, failed("receipt", "JOB_INTAKE_RECEIPT_INVALID")
	}
	return "jobs/intake_receipts/" + intent.OperationID + ".json", raw, nil
}
