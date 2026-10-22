package jobcard

import (
	"context"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobdigest"
)

// InvalidProjection keeps an unreadable card visible without publishing native
// error text, arbitrary YAML values, or a usable application identity.
func InvalidProjection(relative, modified string) map[string]any {
	out := map[string]any{
		"id":    strings.TrimSuffix(path.Base(relative), path.Ext(relative)),
		"valid": false, "validation_errors": []string{"Job Card could not be read safely."},
		"status": "invalid", "visa_signals": map[string]any{}, "digest": nil,
		"compensation":     jobdigest.Compensation{Status: "not_stated", Ranges: []jobdigest.CompensationRange{}},
		"analyzer_version": 0, "analysis_stale": true, "source_chars": 0,
		"normalized_path": relative, "modified_at": modified,
	}
	for _, key := range []string{"company", "title", "source_url", "location", "remote_policy", "seniority", "raw_jd_path", "content_fingerprint"} {
		out[key] = ""
	}
	for _, key := range projectionLists {
		out[key] = []any{}
	}
	return out
}

var projectionLists = []string{"required_skills", "required_platforms", "preferred_skills", "required_capabilities", "manual_overrides", "technology_groups", "experience_requirements", "education_requirements", "responsibilities"}

// Project presents an already bounded, validated fresh Read result. Validation
// failures remain visible; they never become an application or candidate fact.
func Project(ctx context.Context, loaded *Loaded, relative, modified string) (map[string]any, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	if loaded == nil || loaded.Report == nil {
		return nil, &Error{Code: "JOB_CARD_PROJECTION_INVALID"}
	}
	out := InvalidProjection(relative, modified)
	if loaded.Card.Kind != "mapping" {
		return out, nil
	}
	issues := []string{}
	for _, issue := range loaded.Report.Issues {
		issues = append(issues, issue.Field+": "+issue.Message)
	}
	for _, key := range []string{"id", "status", "company", "title", "source_url", "location", "remote_policy", "seniority", "raw_jd_path", "content_fingerprint"} {
		value := get(loaded.Card, key)
		if value.Kind == "string" {
			out[key] = value.Text
		}
	}
	if out["id"] == "" {
		out["id"] = strings.TrimSuffix(path.Base(relative), path.Ext(relative))
	}
	for _, key := range append(append([]string{}, projectionLists...), "visa_signals", "digest", "compensation") {
		value := get(loaded.Card, key)
		if key == "technology_groups" {
			value, _ = technologyGroups(loaded.Card)
		}
		if (key == "digest" || key == "compensation" || key == "visa_signals") && value.Kind != "mapping" {
			continue
		}
		if key != "digest" && key != "compensation" && key != "visa_signals" && value.Kind != "sequence" {
			continue
		}
		if value.Kind == "null" {
			continue
		}
		converted, ok := projectionValue(ctx, value)
		if !ok {
			if err := stopped(ctx); err != nil {
				return nil, err
			}
			return InvalidProjection(relative, modified), nil
		}
		out[key] = converted
	}
	// Match text-mode utf-8-sig display without altering exact-byte read hashes.
	raw := strings.TrimPrefix(loaded.RawText, "\ufeff")
	out["source_chars"] = utf8.RuneCountInString(raw)
	if out["content_fingerprint"] == "" && raw != "" {
		fingerprint, err := applicationanalysis.SourceFingerprint(raw)
		if err != nil {
			return nil, &Error{Code: "JOB_CARD_PROJECTION_INVALID"}
		}
		out["content_fingerprint"] = fingerprint
	}
	if get(loaded.Card, "compensation").Kind != "mapping" {
		compensation, err := jobdigest.DigestCompensation(ctx, raw)
		if err != nil {
			return nil, &Error{Code: "JOB_CARD_PROJECTION_INVALID"}
		}
		out["compensation"] = compensation
	}
	version := get(get(loaded.Card, "digest"), "analyzer_version")
	analyzer := int64(0)
	if version.Kind == "int" {
		var err error
		analyzer, err = strconv.ParseInt(version.Text, 10, 64)
		if err != nil {
			return InvalidProjection(relative, modified), nil
		}
	}
	out["analyzer_version"], out["analysis_stale"] = analyzer, analyzer != jobdigest.AnalyzerVersion
	out["valid"], out["validation_errors"] = loaded.Report.SchemaValid(), issues
	if _, err := json.Marshal(out); err != nil {
		return InvalidProjection(relative, modified), nil
	}
	return out, stopped(ctx)
}

func projectionValue(ctx context.Context, value cataloginput.Value) (any, bool) {
	if ctx.Err() != nil {
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
		out := make([]any, 0, len(value.Items))
		for _, item := range value.Items {
			v, ok := projectionValue(ctx, item)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		return out, true
	case "mapping":
		out := map[string]any{}
		for _, item := range value.Entries {
			if item.Key.Kind != "string" {
				return nil, false
			}
			v, ok := projectionValue(ctx, item.Value)
			if !ok {
				return nil, false
			}
			out[item.Key.Text] = v
		}
		return out, true
	}
	return nil, false
}
