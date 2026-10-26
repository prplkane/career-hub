package jobcard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"unicode"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

// PrepareReanalysis builds one final draft card without publishing it. The
// caller must obtain the fresh digest from the retained raw JD and recheck its
// card/raw/catalog/profile witnesses under the owned writer before one guarded
// replacement. A previously validated card is not itself a write permission.
func PrepareReanalysis(ctx context.Context, loaded *Loaded, fresh *jobdigest.Result) (cataloginput.Value, *Error) {
	if err := stopped(ctx); err != nil {
		return cataloginput.Value{}, err
	}
	if loaded == nil || loaded.Report == nil || !loaded.Report.SchemaValid() || loaded.Card.Kind != "mapping" {
		return cataloginput.Value{}, &Error{Code: "JOB_REANALYSIS_CARD_INVALID"}
	}
	if get(loaded.Card, "status").Text != "draft" {
		return cataloginput.Value{}, &Error{Code: "JOB_REANALYSIS_NOT_DRAFT"}
	}
	if !loaded.RawPresent || textcompat.Strip(strings.TrimPrefix(loaded.RawText, "\ufeff")) == "" || fresh == nil {
		return cataloginput.Value{}, &Error{Code: "JOB_REANALYSIS_SOURCE_INVALID"}
	}
	if get(loaded.Card, "id").Text != reanalysisSourceID(get(loaded.Card, "raw_jd_path").Text) {
		return cataloginput.Value{}, &Error{Code: "JOB_REANALYSIS_SOURCE_MISMATCH"}
	}
	if !knownReanalysisFields(loaded.Card) {
		return cataloginput.Value{}, &Error{Code: "JOB_REANALYSIS_UNSUPPORTED_FIELD"}
	}
	fingerprint, sourceErr := applicationanalysis.SourceFingerprint(strings.TrimPrefix(loaded.RawText, "\ufeff"))
	if sourceErr != nil || fresh.ContentFingerprint != fingerprint {
		return cataloginput.Value{}, &Error{Code: "JOB_REANALYSIS_SOURCE_MISMATCH"}
	}
	card := cloneValue(loaded.Card)
	if digestErr := applyFreshDigest(&card, fresh, "JOB_REANALYSIS_DIGEST_INVALID"); digestErr != nil {
		return cataloginput.Value{}, digestErr
	}

	updates, overrideErr := reanalysisOverrides(loaded.Card)
	if overrideErr != nil {
		return cataloginput.Value{}, overrideErr
	}
	if len(updates) != 0 {
		changed, editErr := editDetails(ctx, &Loaded{Card: card, Report: loaded.Report}, updates, MaxInputBytes, jobdigest.MaxItems)
		if editErr != nil {
			if canceled := stopped(ctx); canceled != nil {
				return cataloginput.Value{}, canceled
			}
			return cataloginput.Value{}, &Error{Code: "JOB_REANALYSIS_OVERRIDE_INVALID"}
		}
		card = changed
	}
	// No file is read here. The callback only proves the transformation retained
	// the source selector; the writer must re-admit the actual file and witnesses.
	rawPath := get(loaded.Card, "raw_jd_path").Text
	report, validateErr := Validate(ctx, card, func(_ context.Context, relative string) (bool, *Error) {
		return relative == rawPath && loaded.RawPresent, nil
	})
	if validateErr != nil || report == nil || !report.SchemaValid() {
		if canceled := stopped(ctx); canceled != nil {
			return cataloginput.Value{}, canceled
		}
		return cataloginput.Value{}, &Error{Code: "JOB_REANALYSIS_RESULT_INVALID"}
	}
	return card, nil
}

// applyFreshDigest changes only an in-memory draft. Both new intake and
// reanalysis use the same v12 card shape; neither call grants file authority.
func applyFreshDigest(card *cataloginput.Value, fresh *jobdigest.Result, invalidCode string) *Error {
	result, resultErr := digestResultValue(fresh, invalidCode)
	if resultErr != nil {
		return resultErr
	}
	fields := get(result, "fields")
	if fields.Kind != "mapping" {
		return &Error{Code: invalidCode}
	}
	for _, name := range []string{"company", "title", "location", "remote_policy", "seniority"} {
		field := get(fields, name)
		value := get(field, "value")
		if field.Kind != "mapping" || value.Kind != "string" {
			return &Error{Code: invalidCode}
		}
		setValue(card, name, value)
	}
	for _, name := range []string{
		"required_skills", "required_platforms", "preferred_skills", "responsibilities",
		"experience_requirements", "education_requirements", "visa_signals",
		"compensation", "technology_groups", "content_fingerprint",
	} {
		value, found := lookup(result, name)
		if !found {
			return &Error{Code: invalidCode}
		}
		setValue(card, name, value)
	}
	// This family is manual-only in the Python source. Reusing an old derived
	// value would silently carry a stale requirement into the fresh card.
	setValue(card, "required_capabilities", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}})
	digest := cataloginput.Value{Kind: "mapping"}
	setValue(&digest, "schema_version", cataloginput.Value{Kind: "int", Text: "1"})
	setValue(&digest, "analyzer_version", cataloginput.Value{Kind: "int", Text: strconv.Itoa(jobdigest.AnalyzerVersion)})
	for _, name := range []string{
		"fields", "mentioned_technologies", "skill_occurrences", "eligibility_gates",
		"classified_requirements", "application_instructions", "contact_candidates",
		"provenance", "analysis_coverage", "unresolved_fields",
	} {
		value, found := lookup(result, name)
		if !found {
			return &Error{Code: invalidCode}
		}
		setValue(&digest, name, value)
	}
	setValue(card, "digest", digest)
	return nil
}

func knownReanalysisFields(card cataloginput.Value) bool {
	known := map[string]bool{}
	for _, name := range []string{
		"id", "status", "company", "title", "source_url", "location", "remote_policy", "seniority",
		"required_skills", "required_platforms", "required_capabilities", "preferred_skills",
		"technology_groups", "experience_requirements", "education_requirements", "responsibilities",
		"compensation", "visa_signals", "raw_jd_path", "notes", "manual_overrides",
		"content_fingerprint", "posting_id", "posting_identity", "intake_idempotency_key", "digest",
	} {
		known[name] = true
	}
	for _, entry := range card.Entries {
		if entry.Key.Kind != "string" || !known[entry.Key.Text] {
			return false
		}
	}
	return true
}

// Python reanalysis requires slugify(Path(raw_jd_path).stem) == job_id. This
// fixed-path check never resolves or opens the selector; the owned reader and
// writer retain the managed-path and native-link checks.
func reanalysisSourceID(rawPath string) string {
	name := path.Base(strings.ReplaceAll(rawPath, "\\", "/"))
	stem := name
	if dot := strings.LastIndexByte(name, '.'); dot > 0 && dot < len(name)-1 {
		stem = name[:dot]
	}
	return slugID(stem)
}

func slugID(stem string) string {
	var slug strings.Builder
	separator := false
	for _, character := range textcompat.Fold(stem) {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || character == '_' {
			if separator && slug.Len() != 0 {
				slug.WriteByte('_')
			}
			slug.WriteRune(character)
			separator = false
		} else {
			separator = true
		}
	}
	value := strings.Trim(slug.String(), "_")
	if value != "" {
		return value
	}
	fingerprint := sha256.Sum256([]byte(stem))
	return "job_" + hex.EncodeToString(fingerprint[:4])
}

func digestResultValue(fresh *jobdigest.Result, invalidCode string) (cataloginput.Value, *Error) {
	raw, err := json.Marshal(fresh)
	if err != nil || len(raw) > jobdigest.MaxOutputBytes {
		return cataloginput.Value{}, &Error{Code: invalidCode}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if decoder.Decode(&result) != nil {
		return cataloginput.Value{}, &Error{Code: invalidCode}
	}
	value, valid := detailValueLimited(result, 0, jobdigest.MaxItems)
	if !valid {
		return cataloginput.Value{}, &Error{Code: invalidCode}
	}
	return value, nil
}

func reanalysisOverrides(card cataloginput.Value) (map[string]any, *Error) {
	updates := map[string]any{}
	list := get(card, "manual_overrides")
	if list.Kind == "null" {
		return updates, nil
	}
	if list.Kind != "sequence" || len(list.Items) > 32 {
		return nil, &Error{Code: "JOB_REANALYSIS_OVERRIDE_INVALID"}
	}
	digest := get(card, "digest")
	for _, item := range list.Items {
		if item.Kind != "string" || !editableDetail(item.Text) {
			return nil, &Error{Code: "JOB_REANALYSIS_OVERRIDE_INVALID"}
		}
		name := item.Text
		old := card
		if name == "eligibility_gates" || name == "classified_requirements" || name == "mentioned_technologies" {
			old = digest
		}
		value, found := lookup(old, name)
		if !found {
			return nil, &Error{Code: "JOB_REANALYSIS_OVERRIDE_INVALID"}
		}
		converted, valid := reanalysisJSONValue(value, 0)
		if !valid {
			return nil, &Error{Code: "JOB_REANALYSIS_OVERRIDE_INVALID"}
		}
		updates[name] = converted
	}
	return updates, nil
}

func reanalysisJSONValue(value cataloginput.Value, depth int) (any, bool) {
	if depth > 32 {
		return nil, false
	}
	switch value.Kind {
	case "null":
		return nil, true
	case "string":
		return value.Text, true
	case "bool":
		return value.Boolean, true
	case "int", "float":
		return json.Number(value.Text), true
	case "sequence":
		items := make([]any, 0, len(value.Items))
		for _, item := range value.Items {
			converted, valid := reanalysisJSONValue(item, depth+1)
			if !valid {
				return nil, false
			}
			items = append(items, converted)
		}
		return items, true
	case "mapping":
		entries := make(map[string]any, len(value.Entries))
		for _, entry := range value.Entries {
			if entry.Key.Kind != "string" {
				return nil, false
			}
			converted, valid := reanalysisJSONValue(entry.Value, depth+1)
			if !valid {
				return nil, false
			}
			entries[entry.Key.Text] = converted
		}
		return entries, true
	}
	return nil, false
}
