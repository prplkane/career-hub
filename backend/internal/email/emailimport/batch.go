package emailimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/email/emailalerts"
	"careerhub.local/backend/internal/email/emailmime"
	"careerhub.local/backend/internal/email/emailoutcomes"
	"careerhub.local/backend/internal/textcompat"
)

const MaxBatchJSONBytes = 36 * 1024 * 1024
const MaxBatchBytes = 25 * 1024 * 1024

// Batch contains only prevalidated, bounded memory. It never persists raw mail.
type Batch struct{ files []batchFile }
type batchFile struct {
	result  FileResult
	content []byte
}
type BatchError struct{ Code string }

func (e *BatchError) Error() string { return e.Code }

type FileError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type FileResult struct {
	FileName                string                `json:"file_name"`
	Status                  string                `json:"status"`
	MessageFingerprint      string                `json:"message_fingerprint"`
	CandidateCount          int                   `json:"candidate_count"`
	ApplicationOutcomeCount int                   `json:"application_outcome_count"`
	Discoveries             []map[string]any      `json:"discoveries"`
	ApplicationOutcomes     []map[string]any      `json:"application_outcomes"`
	Warnings                []emailalerts.Warning `json:"warnings"`
	Error                   *FileError            `json:"error"`
}
type BatchResult struct {
	ContractVersion int            `json:"contract_version"`
	Files           []FileResult   `json:"files"`
	Summary         map[string]int `json:"summary"`
}
type BatchOutcome struct {
	Result                                                    BatchResult
	Committed, CommitUnknown, CleanupFailed, RecoveryRequired bool
}

var batchMessages = map[string]string{
	"EMAIL_IMPORT_FILE_INVALID":       "The email file entry is invalid.",
	"EMAIL_IMPORT_FILE_TOO_LARGE":     "The email file exceeds the per-file limit.",
	"EMAIL_IMPORT_DECODE_FAILED":      "The email file is not valid base64 content.",
	"EMAIL_IMPORT_DISCOVERY_FAILED":   "A parsed candidate could not be stored as a discovery.",
	"EMAIL_IMPORT_OUTCOME_FAILED":     "An application status update could not be stored safely.",
	"EMAIL_EMPTY":                     "Email file is empty.",
	"EMAIL_TOO_LARGE":                 "Email file exceeds the supported size limit.",
	"EMAIL_MIME_LIMIT_EXCEEDED":       "Email has too many MIME parts.",
	"EMAIL_MIME_BYTES_LIMIT_EXCEEDED": "Email structure exceeds the supported parsing budget.",
	"EMAIL_NESTING_LIMIT_EXCEEDED":    "Email nesting exceeds the supported limit.",
	"EMAIL_TEXT_LIMIT_EXCEEDED":       "Email text exceeds the supported limit.",
	"EMAIL_PARSE_FAILED":              "Email could not be parsed safely.",
}

func batchFileError(code string) *FileError {
	message, ok := batchMessages[code]
	if !ok {
		code, message = "EMAIL_PARSE_FAILED", batchMessages["EMAIL_PARSE_FAILED"]
	}
	return &FileError{Code: code, Message: message}
}
func blankFile(name string) FileResult {
	return FileResult{FileName: name, Status: "failed", Discoveries: []map[string]any{},
		ApplicationOutcomes: []map[string]any{}, Warnings: []emailalerts.Warning{}}
}

// PrepareFiles completes all decoding and the total-size check before any
// writer is called. Invalid individual entries remain ordered per-file errors.
// The transport checks duplicate JSON keys and scalar Unicode before this call.
func PrepareFiles(files []json.RawMessage) (*Batch, *BatchError) {
	if len(files) < 1 || len(files) > 10 {
		return nil, &BatchError{"EMAIL_IMPORT_FILE_COUNT_INVALID"}
	}
	b := &Batch{files: make([]batchFile, len(files))}
	total := 0
	for index, raw := range files {
		result := blankFile(fmt.Sprintf("Email %d", index+1))
		var fields map[string]json.RawMessage
		var name, encoded string
		_ = json.Unmarshal(raw, &fields)
		_ = json.Unmarshal(fields["file_name"], &name)
		display := strings.Join(strings.FieldsFunc(name, textcompat.IsSpace), " ")
		if display != "" && utf8.RuneCountInString(display) <= 240 {
			result.FileName = display
		}
		name = textcompat.Strip(name)
		code := ""
		switch {
		case len(fields) != 2 || fields["file_name"] == nil || fields["content_base64"] == nil || name == "" ||
			utf8.RuneCountInString(name) > 240 || strings.ContainsAny(name, "/\\\x00:") ||
			!strings.HasSuffix(textcompat.Fold(name), ".eml") || json.Unmarshal(fields["content_base64"], &encoded) != nil || encoded == "":
			code = "EMAIL_IMPORT_FILE_INVALID"
		case len(encoded) > ((emailmime.MaxEmailBytes+2)/3)*4:
			code = "EMAIL_IMPORT_FILE_TOO_LARGE"
		}
		var content []byte
		if code == "" {
			var err error
			content, err = base64.StdEncoding.DecodeString(encoded)
			if err != nil || strings.ContainsAny(encoded, "\r\n") {
				code = "EMAIL_IMPORT_DECODE_FAILED"
			}
			if code == "" && len(content) > emailmime.MaxEmailBytes {
				code = "EMAIL_IMPORT_FILE_TOO_LARGE"
			}
		}
		if code != "" {
			result.Error = batchFileError(code)
			content = nil
		} else {
			result.FileName = name
			total += len(content)
		}
		b.files[index] = batchFile{result, content}
	}
	if total > MaxBatchBytes {
		return nil, &BatchError{"EMAIL_IMPORT_TOTAL_TOO_LARGE"}
	}
	return b, nil
}

func (out *BatchOutcome) observe(value *applicationwrite.Outcome, problem *applicationwrite.Error) (json.RawMessage, *BatchError) {
	if value != nil {
		out.Committed = out.Committed || value.Committed
		out.CleanupFailed = out.CleanupFailed || value.CleanupFailed
		out.RecoveryRequired = out.RecoveryRequired || value.RecoveryRequired
	}
	raw, _, uncertain := ownerResult(value, problem)
	if uncertain != nil {
		out.CommitUnknown = true
		return nil, &BatchError{"EMAIL_IMPORT_WRITE_UNVERIFIED"}
	}
	return raw, nil
}

// Run shares the native per-unit commit owners with mailbox imports. Earlier
// units may commit before a later failure; uncertainty stops the whole batch.
func (b *Batch) Run(ctx context.Context, writer Writer) (*BatchOutcome, *BatchError) {
	out := &BatchOutcome{Result: BatchResult{ContractVersion: 1, Files: []FileResult{}}}
	if b == nil || len(b.files) == 0 || writer == nil || ctx == nil {
		return out, &BatchError{"EMAIL_IMPORT_PAYLOAD_INVALID"}
	}
	for _, file := range b.files {
		if ctx.Err() != nil {
			return out, &BatchError{"EMAIL_IMPORT_CANCELED"}
		}
		result := file.result
		if result.Error == nil {
			parsed, err := emailalerts.Parse(file.content)
			if err != nil {
				result.Error = batchFileError(err.Error())
			} else {
				result.Status, result.MessageFingerprint = "imported", parsed.MessageFingerprint
				result.CandidateCount, result.ApplicationOutcomeCount = len(parsed.Candidates), len(parsed.ApplicationOutcomes)
				result.Warnings = parsed.Warnings
				if result.Warnings == nil {
					result.Warnings = []emailalerts.Warning{}
				}
				if result.CandidateCount+result.ApplicationOutcomeCount == 0 {
					result.Status = "no_candidates"
				}
				if problem := out.importFile(ctx, writer, parsed, &result); problem != nil {
					return out, problem
				}
			}
		}
		out.Result.Files = append(out.Result.Files, result)
	}
	out.Result.Summary = summarizeBatch(out.Result.Files)
	return out, nil
}

func (out *BatchOutcome) importFile(ctx context.Context, writer Writer, parsed emailalerts.Result, result *FileResult) *BatchError {
	failedOutcomes, failedCandidates := 0, 0
	for _, candidate := range parsed.ApplicationOutcomes {
		if ctx.Err() != nil {
			return &BatchError{"EMAIL_IMPORT_CANCELED"}
		}
		value, problem := writer.RecordEmailApplicationOutcome(ctx, parsed.MessageFingerprint, candidate, parsed.ReceivedAt)
		raw, failure := out.observe(value, problem)
		if failure != nil {
			return failure
		}
		var saved emailoutcomes.Result
		var item map[string]any
		if raw != nil && (json.Unmarshal(raw, &saved) != nil || saved.OutcomeID == "" || !validResolution(saved.Resolution)) {
			out.CommitUnknown = true
			return &BatchError{"EMAIL_IMPORT_WRITE_UNVERIFIED"}
		}
		if raw == nil {
			failedOutcomes++
			item = map[string]any{"ordinal": candidate.Ordinal, "outcome": "failed", "error": batchFileError("EMAIL_IMPORT_OUTCOME_FAILED")}
		} else {
			// Project the documented result, never arbitrary owner fields.
			projected, _ := json.Marshal(saved)
			_ = json.Unmarshal(projected, &item)
			item["ordinal"], item["error"] = candidate.Ordinal, nil
		}
		result.ApplicationOutcomes = append(result.ApplicationOutcomes, item)
	}
	requests, err := ProjectCandidates(parsed, ChannelFile)
	if err != nil {
		return &BatchError{"EMAIL_IMPORT_FILE_INVALID"}
	}
	for index, request := range requests {
		if ctx.Err() != nil {
			return &BatchError{"EMAIL_IMPORT_CANCELED"}
		}
		candidate := parsed.Candidates[index]
		value, problem := writer.CaptureDiscovery(ctx, request)
		raw, failure := out.observe(value, problem)
		if failure != nil {
			return failure
		}
		item := map[string]any{"ordinal": candidate.Ordinal, "outcome": "failed", "error": batchFileError("EMAIL_IMPORT_DISCOVERY_FAILED")}
		if raw == nil {
			failedCandidates++
		} else {
			var captured struct {
				Outcome   string         `json:"outcome"`
				Discovery map[string]any `json:"discovery"`
			}
			if json.Unmarshal(raw, &captured) != nil || captured.Discovery["discovery_id"] == nil ||
				(captured.Outcome != "created" && captured.Outcome != "updated" && captured.Outcome != "duplicate") {
				out.CommitUnknown = true
				return &BatchError{"EMAIL_IMPORT_WRITE_UNVERIFIED"}
			}
			for _, key := range []string{"discovery_id", "state", "title", "company", "location", "observed_url", "posting_identity", "content_scope"} {
				text, ok := captured.Discovery[key].(string)
				if !ok || key == "discovery_id" && text == "" {
					out.CommitUnknown = true
					return &BatchError{"EMAIL_IMPORT_WRITE_UNVERIFIED"}
				}
				item[key] = text
			}
			needsContent, ok := captured.Discovery["needs_content"].(bool)
			if !ok {
				out.CommitUnknown = true
				return &BatchError{"EMAIL_IMPORT_WRITE_UNVERIFIED"}
			}
			item["outcome"], item["error"], item["needs_content"] = captured.Outcome, nil, needsContent
			item["source_provider"], item["source_recommendation"] = candidate.SourceProvider, candidate.SourceRecommendation
		}
		result.Discoveries = append(result.Discoveries, item)
	}
	if failedOutcomes+failedCandidates > 0 {
		result.Status = "partial"
		if failedOutcomes+failedCandidates == result.CandidateCount+result.ApplicationOutcomeCount {
			result.Status = "failed"
		}
		code := "EMAIL_IMPORT_OUTCOME_FAILED"
		if failedCandidates > 0 {
			code = "EMAIL_IMPORT_DISCOVERY_FAILED"
		}
		result.Error = batchFileError(code)
	}
	return nil
}

func summarizeBatch(files []FileResult) map[string]int {
	summary := map[string]int{}
	for _, key := range []string{"files_received", "files_imported", "files_partial", "files_failed", "files_without_candidates", "candidates_found", "application_outcomes_found", "discoveries_created", "discoveries_updated", "discoveries_duplicate", "candidates_failed", "application_outcomes_recorded", "applications_updated", "discoveries_dismissed", "application_outcomes_unlinked", "application_outcomes_needs_review", "application_outcomes_failed"} {
		summary[key] = 0
	}
	summary["files_received"] = len(files)
	for _, file := range files {
		status := file.Status
		if status == "no_candidates" {
			status = "without_candidates"
		}
		summary["files_"+status]++
		summary["candidates_found"] += file.CandidateCount
		summary["application_outcomes_found"] += file.ApplicationOutcomeCount
		for _, item := range file.Discoveries {
			outcome, _ := item["outcome"].(string)
			if outcome == "failed" {
				summary["candidates_failed"]++
			} else {
				summary["discoveries_"+outcome]++
			}
		}
		for _, item := range file.ApplicationOutcomes {
			if item["outcome"] == "failed" {
				summary["application_outcomes_failed"]++
				continue
			}
			summary["application_outcomes_recorded"]++
			if item["application_changed"] == true {
				summary["applications_updated"]++
			}
			if item["discovery_changed"] == true {
				summary["discoveries_dismissed"]++
			}
			switch item["resolution"] {
			case "unmatched", "discovery_dismissed":
				summary["application_outcomes_unlinked"]++
			case "status_conflict", "ambiguous":
				summary["application_outcomes_needs_review"]++
			}
		}
	}
	return summary
}
