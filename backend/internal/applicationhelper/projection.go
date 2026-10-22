package applicationhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/sqlitestore"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vaultread"
)

func canonicalValue(v cataloginput.Value, f field) any {
	for _, key := range f.path {
		if v.Kind != "mapping" {
			return nil
		}
		v = lookup(v, key)
	}
	if f.kind == "boolean" {
		if v.Kind == "bool" {
			return v.Boolean
		}
		return nil
	}
	if v.Kind == "string" {
		if text := textcompat.Strip(v.Text); text != "" {
			return text
		}
	}
	return nil
}
func projection(ctx context.Context, r *reads) (map[string]any, *Error) {
	raw, present, e := readSetting(r, ConfigurationPath)
	if e != nil {
		return nil, e
	}
	enabled, config, e := configuration(raw, present)
	if e != nil {
		return nil, e
	}
	selected, sourceDocs := []field{}, map[string]bool{}
	for _, f := range fields {
		for _, id := range enabled {
			if f.id == id {
				selected = append(selected, f)
				sourceDocs[f.document] = true
			}
		}
	}
	records, revisions, ve := vaultread.HelperSources(ctx, r, sourceDocs["profile"], sourceDocs["eligibility"])
	if ve != nil {
		if ctx.Err() != nil {
			return nil, canceled()
		}
		return nil, sourceUnavailable()
	}
	if sourceDocs["demographics"] {
		raw, present, e := readSetting(r, DemographicsPath)
		if e != nil {
			return nil, sourceUnavailable()
		}
		values, content, e := demographicSettings(raw, present)
		if e != nil {
			return nil, sourceUnavailable()
		}
		us := cataloginput.Value{Kind: "mapping"}
		for _, f := range demographics {
			us.Entries = append(us.Entries, cataloginput.Entry{Key: cataloginput.Value{Kind: "string", Text: f.id}, Value: cataloginput.Value{Kind: "string", Text: values[f.id]}})
		}
		records["demographics"] = cataloginput.Value{Kind: "mapping", Entries: []cataloginput.Entry{{Key: cataloginput.Value{Kind: "string", Text: "US"}, Value: us}}}
		revisions["demographics"] = revision(content)
	}
	raw, present, e = readSetting(r, AnswersPath)
	if e != nil {
		return nil, e
	}
	answers, answerContent, e := answerBank(raw, present)
	if e != nil {
		return nil, e
	}
	resumes, e := resumeShelf(ctx, r)
	if e != nil {
		return nil, e
	}
	items := []any{}
	for _, f := range selected {
		if value := canonicalValue(records[f.document], f); value != nil {
			items = append(items, map[string]any{"id": f.id, "label": f.label, "source": f.source, "value_kind": f.kind, "value": value})
		}
	}
	// Sorted ASCII keys/digests match the source's compact sorted JSON. Answer
	// and resume changes intentionally do not invalidate a profile-field review.
	material, _ := json.Marshal(map[string]any{"configuration_revision": revision(config), "source_revisions": revisions})
	return map[string]any{"contract_version": 2, "revision": revision(material), "answer_revision": revision(answerContent), "fields": items, "resumes": resumes, "answers": publicAnswers(answers)}, nil
}
func resumeShelf(ctx context.Context, r *reads) (result []any, problem *Error) {
	const database = "applications/career_hub.sqlite3"
	result = []any{}
	guard, e := r.root.GuardSQLiteFiles(database)
	if e != nil {
		return nil, sourceUnavailable()
	}
	if guard == nil {
		return result, nil
	}
	defer func() {
		if guard.Close() != nil {
			result, problem = nil, sourceUnavailable()
		}
	}()
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(r.service.root, filepath.FromSlash(database)))
	if err != nil {
		return nil, sourceUnavailable()
	}
	defer func() {
		if db.Close() != nil {
			result, problem = nil, sourceUnavailable()
		}
	}()
	resumes, re := applications.ReadResumeLibrary(ctx, db, applications.ResumeFilters{})
	if re != nil {
		return nil, sourceUnavailable()
	}
	for _, resume := range resumes[:min(len(resumes), 50)] {
		id, e := resumeText(resume.ID)
		if e != nil {
			return nil, e
		}
		label, e := resumeText(resume.DisplayName)
		if e != nil {
			return nil, e
		}
		result = append(result, map[string]any{"id": id, "display_name": label})
	}
	return result, nil
}
func resumeText(value applications.Value) (string, *Error) {
	raw, e := value.MarshalJSON()
	if e != nil {
		return "", sourceUnavailable()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var scalar any
	if decoder.Decode(&scalar) != nil {
		return "", sourceUnavailable()
	}
	switch v := scalar.(type) {
	case string:
		return v, nil
	case nil:
		return "None", nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case json.Number:
		return resumeNumber(v.String())
	}
	return "", sourceUnavailable()
}

func resumeNumber(text string) (string, *Error) {
	// Value.MarshalJSON preserves REAL's decimal marker. The existing pinned
	// YAML scalar kernel supplies Python float repr, including exponent spelling
	// and its different fixed/exponent threshold. No user pattern is interpreted.
	if strings.ContainsAny(text, ".eE") {
		v, e := cataloginput.ParseYAML([]byte("!!float " + text))
		if e != nil || v.Kind != "float" {
			return "", sourceUnavailable()
		}
		return v.Text, nil
	}
	return text, nil
}
