// Package applicationanalysis extracts existing, source-backed application
// instructions and review-only contact candidates. It does not create contacts,
// follow links, infer candidate facts, or claim an application was submitted.
package applicationanalysis

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const MaxSourceBytes = 4 << 20
const maxSourceChars, maxLines, maxItems, maxEvidenceChars = 500000, 10000, 24, 1200
const maxChannelsPerLine = 128

var ErrInput = errors.New("unsupported application analysis input")
var ErrLimit = errors.New("application analysis resource limit exceeded")

type Instruction struct {
	Fingerprint       string `json:"fingerprint"`
	Category          string `json:"category"`
	Confidence        string `json:"confidence"`
	SourceText        string `json:"source_text"`
	LineStart         int    `json:"line_start"`
	LineEnd           int    `json:"line_end"`
	Rule              string `json:"rule"`
	ExtractorVersion  int    `json:"extractor_version"`
	EvidenceTruncated bool   `json:"evidence_truncated"`
}
type Scan struct {
	SchemaVersion      int    `json:"schema_version"`
	ExtractorVersion   int    `json:"extractor_version"`
	SourceFingerprint  string `json:"source_fingerprint"`
	Status             string `json:"status"`
	SourceCharsScanned int    `json:"source_chars_scanned"`
	SourceLinesScanned int    `json:"source_lines_scanned"`
	Truncated          bool   `json:"truncated"`
}
type InstructionResult struct {
	Scan
	Items     []Instruction `json:"items"`
	ItemCount int           `json:"item_count"`
}
type Channel struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}
type ContactCandidate struct {
	CandidateID       string    `json:"candidate_id"`
	ContentSHA256     string    `json:"content_sha256"`
	Name              string    `json:"name"`
	Role              string    `json:"role"`
	Company           string    `json:"company"`
	Channels          []Channel `json:"channels"`
	ContextKind       string    `json:"context_kind"`
	Confidence        string    `json:"confidence"`
	SourceText        string    `json:"source_text"`
	LineStart         int       `json:"line_start"`
	LineEnd           int       `json:"line_end"`
	Rule              string    `json:"rule"`
	ExtractorVersion  int       `json:"extractor_version"`
	EvidenceTruncated bool      `json:"evidence_truncated"`
}
type ContactResult struct {
	Scan
	ContentSHA256  string             `json:"content_sha256"`
	Candidates     []ContactCandidate `json:"candidates"`
	CandidateCount int                `json:"candidate_count"`
}
type Result struct {
	Instructions InstructionResult `json:"instructions"`
	Contacts     ContactResult     `json:"contacts"`
}
type preparedSource struct {
	engine      *matcher
	lines       []string
	scan        Scan
	contentHash string
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func collapsed(value string) string {
	return strings.Join(strings.FieldsFunc(value, textcompat.IsSpace), " ")
}
func headingKey(value string) string { return textcompat.Fold(strings.Trim(collapsed(value), ":")) }
func inSet(key, value string) bool   { return slices.Contains(ruleSets[key], value) }
func containsPhrase(key, value string) bool {
	for _, phrase := range ruleSets[key] {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}
func runePrefix(value string, limit int) string {
	count := 0
	for offset := range value {
		if count == limit {
			return value[:offset]
		}
		count++
	}
	return value
}

// SourceFingerprint hashes the whole accepted source, even if scanning is capped.
func SourceFingerprint(value string) (string, error) {
	if len(value) > MaxSourceBytes {
		return "", ErrLimit
	}
	if !utf8.ValidString(value) {
		return "", ErrInput
	}
	normalized, err := textcompat.NFKC15(value)
	if err != nil {
		return "", ErrInput
	}
	normalized = textcompat.Normalize(normalized)
	if len(normalized) > MaxSourceBytes {
		return "", ErrLimit
	}
	return digest(normalized), nil
}

func instructionFingerprint(category, evidence string) string {
	return digest(category + "\n" + textcompat.Fold(collapsed(evidence)))
}

// InstructionFingerprint shares the extractor's exact category/text identity
// with the source-backed checklist. It does not extract or approve an item.
func InstructionFingerprint(category, evidence string) (string, error) {
	if len(evidence) > MaxSourceBytes || utf8.RuneCountInString(evidence) > maxEvidenceChars {
		return "", ErrLimit
	}
	if !inSet("i.APPLICATION_INSTRUCTION_CATEGORIES", category) || !utf8.ValidString(evidence) || collapsed(evidence) == "" {
		return "", ErrInput
	}
	return instructionFingerprint(category, evidence), nil
}

func prepare(value string) (preparedSource, error) {
	fingerprint, err := SourceFingerprint(value)
	if err != nil {
		return preparedSource{}, err
	}
	bounded := runePrefix(value, maxSourceChars)
	lines := textcompat.SplitLines(bounded)
	lineCount := min(len(lines), maxLines)
	return preparedSource{newMatcher(bounded), lines[:lineCount], Scan{
		SchemaVersion: 1, ExtractorVersion: 1, SourceFingerprint: fingerprint, Status: "analyzed",
		SourceCharsScanned: utf8.RuneCountInString(bounded), SourceLinesScanned: lineCount,
		Truncated: len(bounded) < len(value) || len(lines) > lineCount,
	}, digest(value)}, nil
}

// Analyze returns both complete projections or an error, never a partial success.
// Call only on a verified complete JD, not an email snippet or a multi-job DOM.
func Analyze(source string) (Result, error) {
	prepared, err := prepare(source)
	if err != nil {
		return Result{}, err
	}
	contacts, err := prepared.contacts()
	if err != nil {
		return Result{}, err
	}
	return Result{prepared.instructions(), contacts}, nil
}
func ExtractInstructions(source string) (InstructionResult, error) {
	prepared, err := prepare(source)
	if err != nil {
		return InstructionResult{}, err
	}
	return prepared.instructions(), nil
}
func ExtractContacts(source string) (ContactResult, error) {
	prepared, err := prepare(source)
	if err != nil {
		return ContactResult{}, err
	}
	return prepared.contacts()
}

func (engine *matcher) looksLikeHeading(value string, contacts bool) bool {
	stripped := textcompat.Strip(value)
	if stripped == "" || utf8.RuneCountInString(stripped) > 90 {
		return false
	}
	if _, ok := engine.text(stripped).match("i._LEADING_BULLET", "start"); ok {
		return false
	}
	key := headingKey(stripped)
	if inSet("i.APPLICATION_INSTRUCTION_HEADINGS", key) {
		return true
	}
	if contacts {
		if inSet("c._NOISE_HEADINGS", key) || inSet("c._CONTENT_HEADINGS", key) {
			return true
		}
	} else if inSet("i._NOISE_HEADINGS", key) {
		return true
	}
	words := len(strings.FieldsFunc(stripped, textcompat.IsSpace))
	if strings.HasSuffix(stripped, ":") && words <= 9 {
		return true
	}
	return textcompat.IsUpperString(stripped) && words >= 1 && words <= 9
}
func (engine *matcher) candidateText(stripped string) matchingText {
	text := engine.text(stripped)
	if match, ok := text.match("i._LEADING_BULLET", "start"); ok {
		stripped = stripped[match.end():]
	}
	normalized := collapsed(stripped)
	if normalized == text.source {
		return text
	}
	return engine.text(normalized)
}

type categoryRule struct{ category, rule string }

func candidateCategories(text matchingText) []categoryRule {
	result := make([]categoryRule, 0, 6)
	add := func(condition bool, category, rule string) {
		if condition {
			result = append(result, categoryRule{category, rule})
		}
	}
	add(text.has("i._EMAIL_ADDRESS") && text.has("i._EMAIL_ACTION"), "email_destination", "explicit_email_destination")
	add(text.has("i._EXACT_PHRASE_CONTEXT") && text.has("i._DOUBLE_QUOTED"), "exact_phrase", "exact_phrase_or_subject")
	add(text.has("i._ARTIFACT") && text.has("i._ARTIFACT_ACTION"), "required_artifact", "named_application_artifact")
	add((text.has("i._COVER_LETTER") && text.has("i._FORMAT_ACTION")) || text.has("i._NAMED_QUESTION"), "cover_letter_format", "named_application_response")
	add(text.has("i._EXTERNAL_TARGET") && text.has("i._EXTERNAL_ACTION"), "external_step", "named_external_application_step")
	add(text.has("i._DEADLINE_ACTION") && text.has("i._DEADLINE_MARKER") && text.has("i._DATE_OR_TIME"), "deadline", "explicit_application_deadline")
	return result
}

func (source preparedSource) instructions() InstructionResult {
	result := InstructionResult{Scan: source.scan, Items: make([]Instruction, 0)}
	seen := make(map[string]bool)
	remaining, noise := 0, false
	for index, line := range source.lines {
		stripped := textcompat.Strip(line)
		if stripped == "" {
			continue
		}
		heading := headingKey(stripped)
		if inSet("i.APPLICATION_INSTRUCTION_HEADINGS", heading) {
			remaining = 20
			noise = false
			continue
		}
		if inSet("i._NOISE_HEADINGS", heading) {
			remaining = 0
			noise = true
			continue
		}
		if source.engine.looksLikeHeading(stripped, false) {
			remaining = 0
			noise = false
		}
		text := source.engine.candidateText(stripped)
		lowered := textcompat.Fold(text.source)
		if text.source == "" || inSet("i._GENERIC_APPLICATION_LINES", lowered) || noise || containsPhrase("i._NOISE_PHRASES", lowered) {
			continue
		}
		inSection := remaining > 0
		imperative := text.has("i._ACTION_START") || text.has("i._ACTION_CLAUSE")
		context := text.has("i._APPLICATION_CONTEXT")
		if remaining > 0 {
			remaining--
		}
		if !inSection && !imperative {
			continue
		}
		evidence := runePrefix(stripped, maxEvidenceChars)
		for _, candidate := range candidateCategories(text) {
			if (candidate.category == "external_step" || candidate.category == "deadline") && !inSection && !context {
				continue
			}
			fingerprint := instructionFingerprint(candidate.category, evidence)
			if seen[fingerprint] {
				continue
			}
			seen[fingerprint] = true
			confidence := "review"
			if imperative {
				confidence = "explicit"
			}
			result.Items = append(result.Items, Instruction{fingerprint, candidate.category, confidence, evidence, index + 1, index + 1, candidate.rule, 1, len(evidence) < len(stripped)})
			if len(result.Items) == maxItems {
				break
			}
		}
		if len(result.Items) == maxItems {
			break
		}
	}
	result.ItemCount = len(result.Items)
	result.Truncated = result.Truncated || result.ItemCount >= maxItems
	return result
}
