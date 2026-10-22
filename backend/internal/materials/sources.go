package materials

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/fixedregex"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/resumeplan"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vaultread"
)

func valueField(v cataloginput.Value, key string) cataloginput.Value {
	for _, x := range v.Entries {
		if x.Key.Kind == "string" && x.Key.Text == key {
			return x.Value
		}
	}
	return cataloginput.Value{Kind: "null"}
}

// Matches str(value or "") for scalar metadata, including YAML dates and GPA.
// Arbitrary collection/object repr is not candidate metadata and is refused.
func (p *sourceProjection) scalar(v cataloginput.Value) string {
	switch v.Kind {
	case "null":
		return ""
	case "string", "date", "datetime":
		return v.Text
	case "int":
		if v.Text == "0" {
			return ""
		}
		return v.Text
	case "float":
		if v.Text == "0.0" || v.Text == "-0.0" {
			return ""
		}
		return v.Text
	case "bool":
		if v.Boolean {
			return "True"
		}
		return ""
	}
	p.err = failure("source", "MATERIAL_SOURCE_UNSUPPORTED")
	return ""
}

var graduationYear = fixedregex.MustCompile(map[string]fixedregex.Definition{"year": {Pattern: `\b(?:19|20)\d{2}\b`}})

// credentialInstitution returns the school name for the cover-letter credential
// line ("<institution>, <degree> <major>, Class of <year>"). It keeps the Career
// Vault institution exactly as written: applicant tracking systems and
// recruiters expect the official name, and a guessed abbreviation in a sent
// letter costs more than a long one. Users who prefer a short form write it in
// their Vault.
func credentialInstitution(institution string) string {
	return institution
}

func profileSources(profile, education cataloginput.Value) (Candidate, string, map[string]any, *Error) {
	p := sourceProjection{}
	contact := valueField(profile, "contact")
	f := func(v cataloginput.Value, key string) string { return p.scalar(valueField(v, key)) }
	location := textcompat.Strip(p.first(f(profile, "current_location"), f(contact, "address")))
	flexibility := textcompat.Fold(f(profile, "location_flexibility"))
	if location != "" && (strings.Contains(flexibility, "flexible") || strings.Contains(flexibility, "relocat") || strings.Contains(flexibility, "open")) {
		location += " | Open to Relocation"
	}
	m := model{}
	candidate := m.candidate(map[string]any{"name": f(profile, "name"), "email": f(contact, "email"), "location_line": location, "phone": f(contact, "phone"), "linkedin": f(contact, "linkedin"), "github": f(contact, "github"), "website": f(contact, "website")})
	if m.err != nil {
		return Candidate{}, "", nil, failure("profile", "MATERIAL_PROFILE_INCOMPLETE")
	}
	editor := map[string]any{}
	credential := ""
	firstSchool := false
	for _, key := range []string{"schools", "certifications"} {
		records := []any{}
		for _, r := range valueField(education, key).Items {
			archived := valueField(r, "archived")
			inactive := archived.Kind == "bool" && archived.Boolean
			// The source skips archived records before consuming their metadata.
			if inactive {
				continue
			}
			item := map[string]any{"archived": false}
			fields := []string{"id", "institution", "degree", "major", "location", "graduation_date", "graduation_month", "dates", "gpa"}
			if key == "certifications" {
				fields = []string{"id", "name", "credential", "completed"}
			}
			for _, field := range fields {
				item[field] = f(r, field)
			}
			if key == "schools" {
				honors := []any{}
				for _, x := range valueField(r, "honors").Items {
					honors = append(honors, p.scalar(x))
				}
				item["honors"] = honors
				if !firstSchool {
					firstSchool = true
					institution := credentialInstitution(textcompat.Strip(item["institution"].(string)))
					degree := textcompat.Strip(item["degree"].(string))
					if v, ok := map[string]string{"Bachelor of Science": "B.S.", "Bachelor of Arts": "B.A.", "Master of Science": "M.S.", "Master of Arts": "M.A.", "Associate of Arts": "A.A.", "Associate of Science": "A.S."}[degree]; ok {
						degree = v
					}
					parts := []string{institution, joined(" ", degree, textcompat.Strip(item["major"].(string)))}
					graduation := p.first(item["graduation_date"], item["graduation_month"])
					prepared, e := graduationYear.ForSource(graduation)
					if e != nil {
						return Candidate{}, "", nil, failure("source", "MATERIAL_SOURCE_UNSUPPORTED")
					}
					text, e := prepared.Text(graduation)
					if e != nil {
						return Candidate{}, "", nil, failure("source", "MATERIAL_SOURCE_UNSUPPORTED")
					}
					if match, ok := text.Match("year", ""); ok {
						parts = append(parts, "Class of "+match.Value())
					}
					credential = joined(", ", parts...)
				}
			}
			records = append(records, item)
		}
		editor[key] = records
	}
	if p.err != nil {
		return Candidate{}, "", nil, p.err
	}
	return candidate, credential, editor, nil
}

// ReadSources is read-only within the caller's current-schema transaction. No
// nested connection, implicit bootstrap, source-path fetch or material write.
func ReadSources(ctx context.Context, tx *sql.Tx, reader resumeplan.Source, jobID string, withPlan bool, clock string) (*Sources, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("context", "MATERIAL_CANCELED")
	}
	if tx == nil || reader == nil {
		return nil, failure("configuration", "MATERIAL_SOURCE_REQUIRED")
	}
	m := model{}
	m.job(jobID)
	if m.err != nil {
		return nil, failure("request", "MATERIAL_JOB_ID_INVALID")
	}
	status, fp, e := applications.ResumePlanApplicationInTransaction(ctx, tx, jobID)
	if e != nil {
		return nil, &Error{Stage: e.Stage, Code: e.Code, SourceClass: "ApplicationHistoryError"}
	}
	if !stringSet([]string{"captured", "reviewed", "preparing", "applied", "interview", "offer", "rejected", "withdrawn", "closed"})[status] {
		return nil, failure("application", "MATERIAL_APPLICATION_NOT_READY")
	}
	m.hash(fp, "")
	if m.err != nil {
		return nil, failure("application", "MATERIAL_JOB_FINGERPRINT_INVALID")
	}
	raw, present, pe := reader.ReadFile("jobs/normalized/"+jobID+".yaml", cataloginput.YAMLMaxInputBytes)
	if pe != nil {
		return nil, failure("source", pe.Code)
	}
	if !present {
		return nil, failure("source", "MATERIAL_JOB_UNAVAILABLE")
	}
	card, ye := cataloginput.ParseYAML(raw)
	if ye != nil {
		if ye.Kind == "limit" {
			return nil, failure("source", "MATERIAL_INPUT_LIMIT")
		}
		if ye.Kind == "unsupported" {
			return nil, failure("source", "MATERIAL_JOB_UNSUPPORTED")
		}
		return nil, failure("source", "MATERIAL_JOB_INVALID")
	}
	report, je := jobcard.Validate(ctx, card, func(ctx context.Context, path string) (bool, *jobcard.Error) {
		_, exists, e := reader.ReadFile(path, 4<<20)
		if e != nil {
			return false, &jobcard.Error{Code: e.Code}
		}
		return exists, nil
	})
	if je != nil {
		return nil, failure("source", je.Code)
	}
	if !report.SchemaValid() {
		return nil, failure("source", "MATERIAL_JOB_INVALID")
	}
	if valueField(card, "id").Text != jobID {
		return nil, failure("source", "MATERIAL_JOB_MISMATCH")
	}
	// A valid card cannot silently address a different saved application. Full
	// final-artifact proof and byte lineage are still the separate CH-8 gate.
	if v := valueField(card, "content_fingerprint"); v.Kind == "string" && v.Text != "" && v.Text != fp {
		return nil, failure("source", "MATERIAL_JOB_SOURCE_STALE")
	}
	var catalog *resumeplan.Catalog
	var planEditor *resumeplan.Editor
	if withPlan {
		// A closed load uses no entropy and commits no record; the shared store
		// still requires configured ports. Reuse its fully validated catalog.
		out, e := resumeplan.ApplyInTransaction(ctx, tx, reader, resumeplan.Request{Operation: "load", JobID: jobID}, clock, resumelibrary.SecureEntropy{})
		if e != nil {
			return nil, &Error{Stage: e.Stage, Code: e.Code, SourceClass: e.SourceClass}
		}
		if out == nil || out.Changed {
			return nil, failure("plan", "MATERIAL_PLAN_READ_MUTATED")
		}
		var editor resumeplan.Editor
		if json.Unmarshal(out.Result, &editor) != nil {
			return nil, failure("plan", "MATERIAL_PLAN_INVALID")
		}
		planEditor, catalog = &editor, &editor.Catalog
	} else {
		var re *resumeplan.Error
		catalog, re = resumeplan.ReadCatalog(ctx, reader, jobID)
		if re != nil {
			return nil, &Error{Stage: re.Stage, Code: re.Code, SourceClass: re.SourceClass}
		}
	}
	profile, education, revision, ve := vaultread.MaterialSources(ctx, reader)
	if ve != nil {
		return nil, failure("vault", ve.Code)
	}
	candidate, credential, educationEditor, me := profileSources(profile, education)
	if me != nil {
		return nil, me
	}
	p := sourceProjection{}
	s := &Sources{JobID: jobID, Company: p.scalar(valueField(card, "company")), RoleTitle: p.scalar(valueField(card, "title")), JobFingerprint: fp, Candidate: candidate, ProfileRevision: revision, CredentialLine: credential, Catalog: *catalog, Education: educationEditor}
	s.Plan = planEditor
	if p.err != nil {
		return nil, p.err
	}
	return s, nil
}
