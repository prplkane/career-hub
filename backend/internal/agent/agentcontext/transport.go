package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobcard"
)

// ParseTaskJSON is shared by local CLI and HTTP adapters. Check scalar escapes
// before encoding/json could silently replace an unpaired UTF-16 surrogate.
func ParseTaskJSON(ctx context.Context, raw []byte, purpose string) (*TaskRequest, *Error) {
	if len(raw) == 0 || len(raw) > 4*MaxTaskJSONCharacters || utf8.RuneCount(raw) > MaxTaskJSONCharacters {
		return nil, failure("TASK_INVALID")
	}
	if _, e := decodeOrderedContextJSON(ctx, raw); e != nil {
		return nil, e
	}
	var value map[string]any
	if decodeNumbers(raw, &value) != nil || value == nil {
		return nil, failure("TASK_INVALID")
	}
	return NormalizeTask(value, purpose)
}

// ValidateSavedResponse binds the returned packet to the requested purpose,
// task, registry policy and saved job, and checks its ordered packet identity.
// It cannot independently verify the snapshot hash without private source data.
func ValidateSavedResponse(ctx context.Context, raw []byte, job, purpose, registry string, task *TaskRequest) *Error {
	return validatePacketResponse(ctx, raw, job, purpose, registry, task, true, "", false)
}

func ValidateSourceResponse(ctx context.Context, raw []byte, source SourceRequest, registry string) *Error {
	if e := ValidateSourceRequest(source); e != nil {
		return e
	}
	fingerprint, e := applicationanalysis.SourceFingerprint(source.Text)
	if e != nil || len(fingerprint) < 12 {
		return failure("SOURCE_INVALID")
	}
	return validatePacketResponse(ctx, raw, "advisory_"+fingerprint[:12], "assessment", registry, nil, false, fingerprint, false)
}

// ValidatePreparationResponse checks the real write receipt, not a read-only
// packet with a substituted operation. Posting-ID dedup may select an older JD;
// only a newly created card must match the supplied source fingerprint.
func ValidatePreparationResponse(ctx context.Context, raw []byte, job, purpose, registry string, task *TaskRequest, source *SourceRequest) *Error {
	fingerprint := ""
	if source == nil {
		if !jobcard.ValidID(job) {
			return failure("RESPONSE_INVALID")
		}
	} else {
		if job != "" || ValidateSourceRequest(*source) != nil {
			return failure("SOURCE_INVALID")
		}
		text, e := jobcard.IntakeDigestSource(source.Text)
		if e != nil {
			return failure("SOURCE_INVALID")
		}
		var err error
		fingerprint, err = applicationanalysis.SourceFingerprint(text)
		if err != nil {
			return failure("SOURCE_INVALID")
		}
	}
	return validatePacketResponse(ctx, raw, job, purpose, registry, task, true, fingerprint, true)
}

func validatePacketResponse(ctx context.Context, raw []byte, job, purpose, registry string, task *TaskRequest, persisted bool, fingerprint string, prepare bool) *Error {
	p, e := decodeOrderedContextJSON(ctx, raw)
	if e != nil {
		return e
	}
	bad := func() *Error { return failure("RESPONSE_INVALID") }
	if !ValidPurpose(purpose) || registry != "off" && registry != "exact-auto" || p.Kind != "mapping" {
		return bad()
	}
	operation := "context"
	if purpose == "assessment" {
		operation = "analyze"
	}
	if prepare {
		operation = "prepare"
	}
	isText := func(v packetValue, text string) bool { return v.Kind == "string" && v.Text == text }
	isInt := func(v packetValue, n int) bool { return v.Kind == "int" && v.Text == strconv.Itoa(n) }
	get := func(key string) packetValue { return packetGet(p, key, packetNull()) }
	card, receipt := get("job"), get("write_receipt")
	cardID := packetGet(card, "id", packetNull())
	idMatches := isText(cardID, job)
	if prepare && job == "" {
		idMatches = cardID.Kind == "string" && jobcard.ValidID(cardID.Text)
	}
	if !isInt(get("schema_version"), 1) || !isInt(get("contract_version"), 4) || !isText(get("purpose"), purpose) || !isText(get("operation"), operation) ||
		!idMatches || packetGet(card, "persisted", packetNull()).Kind != "bool" || packetGet(card, "persisted", packetNull()).Boolean != persisted ||
		!isText(packetGet(get("registry"), "mode", packetNull()), registry) {
		return bad()
	}
	if prepare {
		if !validPreparationReceipt(receipt, get("application"), job != "") {
			return bad()
		}
		if packetGet(receipt, "job_created", packetNull()).Boolean && !isText(packetGet(get("context_snapshot"), "job_fingerprint", packetNull()), fingerprint) {
			return bad()
		}
	} else if !isText(packetGet(receipt, "result", packetNull()), "read_only") || packetGet(receipt, "changed", packetNull()).Kind != "bool" || packetGet(receipt, "changed", packetNull()).Boolean {
		return bad()
	}
	if !persisted && (get("application").Kind != "null" || !isText(packetGet(get("context_snapshot"), "job_fingerprint", packetNull()), fingerprint)) {
		return bad()
	}
	expected, e := TaskPayload(purpose, task)
	if e != nil {
		return e
	}
	want, e := decodeOrderedContextJSON(ctx, expected)
	if e != nil {
		return e
	}
	wantRaw, e := encodeOrderedContextJSON(ctx, want, true)
	if e != nil {
		return e
	}
	gotRaw, e := encodeOrderedContextJSON(ctx, get("task"), true)
	if e != nil || !bytes.Equal(wantRaw, gotRaw) {
		return bad()
	}
	snapshot := packetGet(get("context_snapshot"), "id", packetNull())
	if snapshot.Kind != "string" || !strings.HasPrefix(snapshot.Text, "sha256:") || len(snapshot.Text) != 71 {
		return bad()
	}
	for _, c := range snapshot.Text[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return bad()
		}
	}
	if len(p.Entries) == 0 || p.Entries[len(p.Entries)-1].Key.Text != "context_metrics" {
		return bad()
	}
	metrics := get("context_metrics")
	p.Entries = p.Entries[:len(p.Entries)-1]
	before, e := encodeOrderedContextJSON(ctx, p, false)
	if e != nil {
		return e
	}
	count := utf8.RuneCount(before)
	if !isText(packetGet(metrics, "packet_fingerprint", packetNull()), packetHash(before)) || !isInt(packetGet(metrics, "characters_before_metrics", packetNull()), count) || !isInt(packetGet(metrics, "approximate_tokens_at_four_characters", packetNull()), (count+3)/4) {
		return bad()
	}
	return nil
}

func validPreparationReceipt(receipt, application packetValue, saved bool) bool {
	if receipt.Kind != "mapping" || len(receipt.Entries) != 7 {
		return false
	}
	get := func(key string) packetValue { return packetGet(receipt, key, packetValue{}) }
	created, reused, changed, applied := get("job_created"), get("job_reused"), get("changed"), get("applied_recorded")
	if created.Kind != "bool" || reused.Kind != "bool" || changed.Kind != "bool" || applied.Kind != "bool" || applied.Boolean || created.Boolean == reused.Boolean || saved && created.Boolean {
		return false
	}
	before, after, result := get("status_before"), get("status_after"), get("result")
	expectedAfter := "preparing"
	if before.Kind == "string" {
		switch before.Text {
		case "preparing", "applied", "interview", "offer":
			expectedAfter = before.Text
		case "captured", "reviewed", "rejected", "withdrawn", "closed":
		default:
			return false
		}
	} else if before.Kind != "null" {
		return false
	}
	if created.Boolean && before.Kind != "null" || after.Kind != "string" || after.Text != expectedAfter || result.Kind != "string" {
		return false
	}
	expectedChanged := created.Boolean || before.Kind == "null" || before.Text != after.Text
	expectedResult := "no_change_needed"
	if expectedChanged {
		expectedResult = "prepared"
	}
	status := packetGet(application, "current_status", packetNull())
	return changed.Boolean == expectedChanged && result.Text == expectedResult && status.Kind == "string" && status.Text == after.Text
}

// TaskWireJSON preserves the validated request without its response-only marker.
func TaskWireJSON(task *TaskRequest) (json.RawMessage, *Error) {
	if task == nil {
		return nil, nil
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if e := encoder.Encode(task.fields); e != nil {
		return nil, failure("TASK_INVALID")
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
