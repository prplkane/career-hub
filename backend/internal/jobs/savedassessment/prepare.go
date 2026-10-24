// Package savedassessment composes the advisory saved-job workflow. It never
// starts a writer, submits an application, edits a JD, or creates candidate facts.
package savedassessment

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/discovery/discoverypreview"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/evaluationinput"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobevaluation"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

const WorkflowVersion = 1
const maxArtifactBytes = 2 << 20

type Error struct{ Stage, Code, SourceClass, SourceMessage string }

func (*Error) Error() string                { return "Saved-job assessment could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func fail(code string) *Error               { return &Error{Stage: "assessment", Code: "SAVED_ASSESSMENT_" + code} }
func active(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return fail("CANCELED")
	}
	return nil
}

type Request struct {
	Operation string
	Fields    map[string]any
	// Month is host-selected. No ambient clock or caller-selected rule versions.
	Month assessmentcontext.Month
}

func (Request) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted saved assessment request]")
}

type Prepared struct {
	id, status, fingerprint                        string
	analyzer                                       int64
	register                                       bool
	operation, finding, resolution, note, expected string
	revisions                                      assessmentcontext.Revisions
	snapshot                                       applications.JobSnapshot
	input                                          jobevaluation.Input
	evaluate                                       func(context.Context, jobevaluation.Input) (*jobevaluation.Result, *jobevaluation.Error)
}

func (*Prepared) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted prepared saved assessment]")
}
func field(value cataloginput.Value, name string) cataloginput.Value {
	for _, e := range value.Entries {
		if e.Key.Kind == "string" && e.Key.Text == name {
			return e.Value
		}
	}
	return cataloginput.Value{Kind: "null"}
}
func request(r Request) (*Prepared, *Error) {
	keys := []string{"job_id", "register_application"}
	if r.Operation == "resolve" {
		keys = append(keys, "finding_key", "resolution", "note", "expected_assessment_revision")
	} else if r.Operation != "load" {
		return nil, fail("REQUEST_INVALID")
	}
	if !r.Month.Valid() || r.Fields == nil {
		return nil, fail("REQUEST_INVALID")
	}
	size := 0
	for k, v := range r.Fields {
		if !slices.Contains(keys, k) {
			return nil, fail("REQUEST_INVALID")
		}
		if k == "register_application" {
			if _, ok := v.(bool); !ok {
				return nil, fail("REQUEST_INVALID")
			}
			continue
		}
		s, ok := v.(string)
		if !ok || !utf8.ValidString(s) || len(s) > 16<<10 {
			return nil, fail("REQUEST_INVALID")
		}
		size += len(s)
	}
	if size > 16<<10 {
		return nil, fail("REQUEST_LIMIT")
	}
	id, _ := r.Fields["job_id"].(string)
	if id == "" || id == "." || id == ".." || len(id) > 256 || textcompat.Strip(id) != id ||
		strings.ContainsAny(id, "/\\:\x00") {
		return nil, fail("JOB_ID_INVALID")
	}
	p := &Prepared{id: id, operation: r.Operation, register: true, evaluate: jobevaluation.Evaluate}
	if v, ok := r.Fields["register_application"].(bool); ok {
		p.register = v
	}
	if p.operation == "resolve" {
		p.finding, _ = r.Fields["finding_key"].(string)
		p.finding = textcompat.Strip(p.finding)
		p.resolution, _ = r.Fields["resolution"].(string)
		p.note, _ = r.Fields["note"].(string)
		p.expected, _ = r.Fields["expected_assessment_revision"].(string)
		if !validSHA(p.expected) || p.finding == "" || len(p.finding) > 160 ||
			!slices.Contains([]string{"confirmed", "resolved", "not_applicable", "accepted_risk"}, p.resolution) {
			return nil, fail("RESOLUTION_INVALID")
		}
	}
	return p, nil
}
func validSHA(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, c := range value[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Prepare always validates the whole current Job Card, Vault and effective
// registry, even when SQLite later supplies a cache hit. Sources must retain an
// immutable byte/presence read set and recheck it before the owner's commit.
func Prepare(ctx context.Context, data, resources evaluationinput.Source, r Request) (*Prepared, *Error) {
	if p := active(ctx); p != nil {
		return nil, p
	}
	p, err := request(r)
	if err != nil {
		return nil, err
	}
	if data == nil || resources == nil {
		return nil, fail("SOURCE_UNAVAILABLE")
	}
	loaded, ce := jobcard.Read(ctx, data, "jobs/normalized/"+p.id+".yaml")
	if ce != nil {
		return nil, fail(ce.Code)
	}
	if !loaded.Report.SchemaValid() {
		return nil, fail("JOB_CARD_INVALID")
	}
	if field(loaded.Card, "id").Text != p.id {
		return nil, fail("CARD_ID_MISMATCH")
	}
	p.status = field(loaded.Card, "status").Text
	p.fingerprint = hex.EncodeToString(loaded.CardFileSHA256[:])
	rawFingerprint, ae := applicationanalysis.SourceFingerprint(strings.TrimPrefix(loaded.RawText, "\ufeff"))
	if ae != nil {
		return nil, fail("SOURCE_INVALID")
	}
	storedFingerprint := field(loaded.Card, "content_fingerprint")
	if storedFingerprint.Kind == "string" && storedFingerprint.Text != "" && storedFingerprint.Text != rawFingerprint {
		return nil, fail("SOURCE_CHANGED")
	}
	analyzer := field(field(loaded.Card, "digest"), "analyzer_version")
	if analyzer.Kind == "int" {
		var er error
		p.analyzer, er = strconv.ParseInt(analyzer.Text, 10, 64)
		if er != nil || p.analyzer < 0 {
			return nil, fail("ANALYZER_RANGE")
		}
	}
	p.snapshot = applications.JobSnapshot{JobID: p.id, Company: textcompat.Strip(field(loaded.Card, "company").Text),
		Title: textcompat.Strip(field(loaded.Card, "title").Text), SourceURL: textcompat.Strip(field(loaded.Card, "source_url").Text),
		ContentFingerprint: rawFingerprint}
	today := time.Date(r.Month.Year, time.Month(r.Month.Number), 1, 0, 0, 0, 0, time.UTC)
	inputs, ee := evaluationinput.Prepare(ctx, data, resources, today)
	if ee != nil {
		return nil, fail(ee.Code)
	}
	raw, present, re := data.ReadFile("career_vault/hard_blocker_policy.yaml", cataloginput.YAMLMaxInputBytes)
	if re != nil {
		return nil, fail("POLICY_READ_FAILED")
	}
	policy := cataloginput.Value{Kind: "null"}
	if present {
		var pe *cataloginput.YAMLError
		policy, pe = cataloginput.ParseYAML(raw)
		if pe != nil {
			return nil, fail("POLICY_INVALID")
		}
	}
	// This is the same pure persisted-policy validator as discovery previews;
	// importing it does not invoke a discovery reader, fetcher or cache owner.
	policy, errPolicy := discoverypreview.Policy(ctx, policy, present)
	if errPolicy != nil {
		return nil, fail("POLICY_INVALID")
	}
	revisions, ie := assessmentcontext.ReadSavedRevisions(ctx, data, resources, r.Month,
		assessmentcontext.SavedVersions{Evaluator: jobevaluation.Version, Validation: jobcard.ValidationVersion, Workflow: WorkflowVersion},
		hex.EncodeToString(loaded.RawFileSHA256[:]))
	if ie != nil {
		return nil, fail(ie.Code)
	}
	p.revisions = revisions
	p.input = jobevaluation.Input{Job: loaded.Card, Profile: inputs.Candidate.Eligibility, Policy: policy,
		Index: inputs.Candidate.Index, Concepts: inputs.Concepts, Expansions: inputs.Expansions, DurationMonths: inputs.Candidate.DurationMonths}
	if err := active(ctx); err != nil {
		return nil, err
	}
	return p, nil
}
