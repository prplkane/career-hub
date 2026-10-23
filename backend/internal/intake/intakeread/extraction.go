// Package intakeread validates existing, untrusted Career Vault intake reports.
// It is a pure read model: callers own file admission, freshness and cleanup.
package intakeread

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
)

// MaxExtractionBytes is the current YAML adapter ceiling, not the source
// application's file limit. A larger persisted report must be classified as
// an input-limit refusal at the future read owner, never as an absent digest.
const MaxExtractionBytes = cataloginput.YAMLMaxInputBytes

// Extraction contains the strict-valid fields needed by digest indexing and
// later review validation. All other source fields are checked, not projected.
type Extraction struct {
	SchemaVersion      string
	ExtractorVersion   string
	RegistryRevision   string
	Source             Source
	Blocks             []Block
	SectionHints       []SectionHint
	TechnologyMentions []TechnologyMention
	DateCandidates     []DateCandidate
}

type Source struct {
	ID       string
	Path     string
	FileType string
	SHA256   string
}

type Block struct {
	ID, Order, Kind, Text string
	Style                 *string
	IsBullet              bool
}

type SectionHint struct {
	SectionType, SourceText, SourceID, BlockID, Extractor string
}

type TechnologyMention struct {
	TechnologyID, CanonicalName, SourceText, SourceID, BlockID, Extractor string
}

type DateCandidate struct {
	SourceText, StartText, SourceID, BlockID string
	EndText                                  *string
	Current                                  bool
}

// Error contains only fixed categories; it never retains source text, a path,
// or a YAML parser's raw error message.
type Error struct{ Code string }

func (*Error) Error() string { return "intake extraction could not be validated" }

type fieldRule struct {
	required bool
	valid    func(cataloginput.Value) bool
}

type fields map[string]cataloginput.Value

func object(v cataloginput.Value, rules map[string]fieldRule) (fields, bool) {
	if v.Kind != "mapping" {
		return nil, false
	}
	result := make(fields, len(v.Entries))
	for _, entry := range v.Entries {
		// PyYAML has already applied duplicate-key replacement at the shared
		// input boundary. A non-string model key cannot name a model field.
		if entry.Key.Kind != "string" {
			return nil, false
		}
		if _, known := rules[entry.Key.Text]; !known {
			return nil, false
		}
		result[entry.Key.Text] = entry.Value
	}
	for key, rule := range rules {
		value, present := result[key]
		if !present {
			if rule.required {
				return nil, false
			}
			continue
		}
		if !rule.valid(value) {
			return nil, false
		}
	}
	return result, true
}

func sequence(element func(cataloginput.Value) bool) func(cataloginput.Value) bool {
	return func(v cataloginput.Value) bool {
		if v.Kind != "sequence" {
			return false
		}
		for _, item := range v.Items {
			if !element(item) {
				return false
			}
		}
		return true
	}
}

func nullable(valid func(cataloginput.Value) bool) func(cataloginput.Value) bool {
	return func(v cataloginput.Value) bool { return v.Kind == "null" || valid(v) }
}

func text(v cataloginput.Value) (string, bool) {
	switch v.Kind {
	case "string":
		return v.Text, true
	case "bytes":
		if utf8.Valid(v.Bytes) {
			return string(v.Bytes), true
		}
	}
	return "", false
}

func validText(v cataloginput.Value) bool { _, ok := text(v); return ok }

func oneOf(choices ...string) func(cataloginput.Value) bool {
	return func(v cataloginput.Value) bool {
		s, ok := text(v)
		if !ok {
			return false
		}
		for _, choice := range choices {
			if s == choice {
				return true
			}
		}
		return false
	}
}

var integerText = regexp.MustCompile(`^[+-]?[0-9]+(?:_[0-9]+)*(?:\.[0-9]+)?$`)

// Pydantic's ordinary int fields coerce integral booleans, floats, byte text
// and numeric strings. Preserve their decimal value without an int64 ceiling.
func integer(v cataloginput.Value) (string, bool) {
	switch v.Kind {
	case "bool":
		if v.Boolean {
			return "1", true
		}
		return "0", true
	case "float":
		value, err := strconv.ParseFloat(v.Text, 64)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) || math.Trunc(value) != value {
			return "", false
		}
		whole, _ := new(big.Float).SetFloat64(value).Int(nil)
		return whole.String(), true
	case "int", "string", "bytes":
		var s string
		if v.Kind == "int" {
			s = v.Text
		} else {
			var ok bool
			s, ok = text(v)
			if !ok {
				return "", false
			}
			s = strings.TrimSpace(s)
		}
		if !integerText.MatchString(s) {
			return "", false
		}
		s = strings.ReplaceAll(s, "_", "")
		if before, after, decimal := strings.Cut(s, "."); decimal {
			if strings.Trim(after, "0") != "" {
				return "", false
			}
			s = before
		}
		whole, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return "", false
		}
		return whole.String(), true
	}
	return "", false
}

func validInteger(v cataloginput.Value) bool { _, ok := integer(v); return ok }

func nonnegativeInteger(v cataloginput.Value) bool {
	s, ok := integer(v)
	return ok && !strings.HasPrefix(s, "-")
}

func validBoolean(v cataloginput.Value) bool {
	switch v.Kind {
	case "bool":
		return true
	case "int":
		value, ok := integer(v)
		return ok && (value == "0" || value == "1")
	case "float":
		value, err := strconv.ParseFloat(v.Text, 64)
		return err == nil && (value == 0 || value == 1)
	case "string", "bytes":
		value, ok := text(v)
		if !ok {
			return false
		}
		switch strings.ToLower(value) {
		case "0", "1", "true", "false", "t", "f", "yes", "no", "y", "n", "on", "off":
			return true
		}
	}
	return false
}

var sourceRules = map[string]fieldRule{
	"id":        {true, validText},
	"path":      {true, validText},
	"file_type": {true, validText},
	"sha256":    {true, validText},
}

func source(v cataloginput.Value) bool { _, ok := object(v, sourceRules); return ok }

var sourceRefRules = map[string]fieldRule{
	"source_id": {true, validText},
	"block_id":  {true, validText},
	"start":     {false, nullable(validInteger)},
	"end":       {false, nullable(validInteger)},
}

func sourceRef(v cataloginput.Value) bool { _, ok := object(v, sourceRefRules); return ok }

var blockRules = map[string]fieldRule{
	"id":        {true, validText},
	"order":     {true, nonnegativeInteger},
	"kind":      {true, validText},
	"text":      {true, validText},
	"style":     {false, nullable(validText)},
	"is_bullet": {false, validBoolean},
}

func block(v cataloginput.Value) bool { _, ok := object(v, blockRules); return ok }

var sectionHintRules = map[string]fieldRule{
	"section_type": {true, validText},
	"source_text":  {true, validText},
	"source_ref":   {true, sourceRef},
	"extractor":    {false, validText},
}

func sectionHint(v cataloginput.Value) bool { _, ok := object(v, sectionHintRules); return ok }

var mentionRules = map[string]fieldRule{
	"technology_id":  {true, validText},
	"canonical_name": {true, validText},
	"source_text":    {true, validText},
	"source_ref":     {true, sourceRef},
	"extractor":      {false, validText},
}

func mention(v cataloginput.Value) bool { _, ok := object(v, mentionRules); return ok }

var resolutionIssueRules = map[string]fieldRule{
	"status":                 {true, oneOf("exact_match", "alias_match", "ambiguous", "unmatched", "invalid_reference")},
	"source_text":            {true, validText},
	"source_ref":             {true, sourceRef},
	"candidate_ids":          {false, sequence(validText)},
	"archived_candidate_ids": {false, sequence(validText)},
}

func resolutionIssue(v cataloginput.Value) bool {
	_, ok := object(v, resolutionIssueRules)
	return ok
}

var dateCandidateRules = map[string]fieldRule{
	"source_text": {true, validText},
	"start_text":  {true, validText},
	"end_text":    {false, nullable(validText)},
	"current":     {false, validBoolean},
	"source_ref":  {true, sourceRef},
}

func dateCandidate(v cataloginput.Value) bool { _, ok := object(v, dateCandidateRules); return ok }

var bulletCandidateRules = map[string]fieldRule{
	"id":         {true, validText},
	"text":       {true, validText},
	"source_ref": {true, sourceRef},
	"extractor":  {false, validText},
}

func bulletCandidate(v cataloginput.Value) bool {
	_, ok := object(v, bulletCandidateRules)
	return ok
}

var extractionRules = map[string]fieldRule{
	"schema_version":               {false, validInteger},
	"status":                       {false, oneOf("extracted")},
	"extractor_version":            {false, validText},
	"registry_revision":            {false, validText},
	"source":                       {true, source},
	"blocks":                       {true, sequence(block)},
	"section_hints":                {true, sequence(sectionHint)},
	"technology_ids":               {true, sequence(validText)},
	"technology_mentions":          {true, sequence(mention)},
	"technology_resolution_issues": {false, sequence(resolutionIssue)},
	"date_candidates":              {true, sequence(dateCandidate)},
	"bullet_candidates":            {true, sequence(bulletCandidate)},
	"unresolved_fields":            {true, sequence(validText)},
}

// ParseExtraction validates the entire persisted ExtractionReport shape using
// source-compatible YAML construction and Pydantic's common scalar coercions.
// The future index owner must still check source ID against the directory and
// bound all files/read witnesses; this pure function neither opens nor writes.
func ParseExtraction(raw []byte) (Extraction, *Error) {
	value, yamlError := cataloginput.ParseYAML(raw)
	if yamlError != nil {
		code := "EXTRACTION_YAML_INVALID"
		if yamlError.Kind == "limit" {
			code = "EXTRACTION_INPUT_LIMIT"
		} else if yamlError.Kind == "unsupported" {
			code = "EXTRACTION_INPUT_UNSUPPORTED"
		}
		return Extraction{}, &Error{Code: code}
	}
	root, valid := object(value, extractionRules)
	if !valid {
		return Extraction{}, &Error{Code: "EXTRACTION_MODEL_INVALID"}
	}
	sourceFields, _ := object(root["source"], sourceRules)
	result := Extraction{
		SchemaVersion:    "2",
		ExtractorVersion: "vault-intake-0.2",
		Source: Source{
			ID:       stringField(sourceFields, "id"),
			Path:     stringField(sourceFields, "path"),
			FileType: stringField(sourceFields, "file_type"),
			SHA256:   stringField(sourceFields, "sha256"),
		},
	}
	if v, found := root["schema_version"]; found {
		result.SchemaVersion, _ = integer(v)
	}
	if v, found := root["extractor_version"]; found {
		result.ExtractorVersion, _ = text(v)
	}
	if v, found := root["registry_revision"]; found {
		result.RegistryRevision, _ = text(v)
	}
	for _, item := range root["blocks"].Items {
		f, _ := object(item, blockRules)
		block := Block{ID: stringField(f, "id"), Kind: stringField(f, "kind"), Text: stringField(f, "text")}
		block.Order, _ = integer(f["order"])
		if value, found := f["style"]; found && value.Kind != "null" {
			style, _ := text(value)
			block.Style = &style
		}
		if value, found := f["is_bullet"]; found {
			block.IsBullet = boolean(value)
		}
		result.Blocks = append(result.Blocks, block)
	}
	for _, item := range root["section_hints"].Items {
		f, _ := object(item, sectionHintRules)
		ref, _ := object(f["source_ref"], sourceRefRules)
		result.SectionHints = append(result.SectionHints, SectionHint{
			SectionType: stringField(f, "section_type"),
			SourceText:  stringField(f, "source_text"),
			SourceID:    stringField(ref, "source_id"),
			BlockID:     stringField(ref, "block_id"),
			Extractor:   optionalStringField(f, "extractor"),
		})
	}
	for _, item := range root["technology_mentions"].Items {
		f, _ := object(item, mentionRules)
		ref, _ := object(f["source_ref"], sourceRefRules)
		result.TechnologyMentions = append(result.TechnologyMentions, TechnologyMention{
			TechnologyID: stringField(f, "technology_id"), CanonicalName: stringField(f, "canonical_name"),
			SourceText: stringField(f, "source_text"), SourceID: stringField(ref, "source_id"),
			BlockID: stringField(ref, "block_id"), Extractor: optionalStringField(f, "extractor"),
		})
	}
	for _, item := range root["date_candidates"].Items {
		f, _ := object(item, dateCandidateRules)
		ref, _ := object(f["source_ref"], sourceRefRules)
		candidate := DateCandidate{
			SourceText: stringField(f, "source_text"), StartText: stringField(f, "start_text"),
			SourceID: stringField(ref, "source_id"), BlockID: stringField(ref, "block_id"),
		}
		if value, found := f["end_text"]; found && value.Kind != "null" {
			end, _ := text(value)
			candidate.EndText = &end
		}
		if value, found := f["current"]; found {
			candidate.Current = boolean(value)
		}
		result.DateCandidates = append(result.DateCandidates, candidate)
	}
	return result, nil
}

func stringField(record fields, key string) string {
	value, _ := text(record[key])
	return value
}

func optionalStringField(record fields, key string) string {
	value, found := record[key]
	if !found || value.Kind == "null" {
		return ""
	}
	text, _ := text(value)
	return text
}

func boolean(value cataloginput.Value) bool {
	switch value.Kind {
	case "bool":
		return value.Boolean
	case "int", "float":
		integer, _ := integer(value)
		return integer == "1"
	case "string", "bytes":
		text, _ := text(value)
		switch strings.ToLower(text) {
		case "1", "true", "t", "yes", "y", "on":
			return true
		}
	}
	return false
}
