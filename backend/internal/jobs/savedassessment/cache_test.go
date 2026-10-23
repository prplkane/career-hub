package savedassessment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobevaluation"
)

func cacheFixture(t *testing.T) (*Prepared, *jobevaluation.Result) {
	t.Helper()
	p := &Prepared{id: "one", status: "draft", fingerprint: strings.Repeat("a", 64), analyzer: 11,
		revisions: assessmentcontext.Revisions{Candidate: "ch-candidate-v2:sha256:" + strings.Repeat("b", 64), Context: "ch-saved-context-v2:sha256:" + strings.Repeat("c", 64)}}
	a := &jobevaluation.Result{JobID: "one", SchemaVersion: 2, ApplicationAllowed: true, Lane: "consider"}
	return p, a
}
func TestSavedArtifactIntegrityAndBounds(t *testing.T) {
	p, a := cacheFixture(t)
	record := p.record("2026-09-09T12:00:00Z")
	raw, e := pack(record, a, p.status)
	if e != nil {
		t.Fatal(e)
	}
	record.Artifact = raw
	if p.restore(record) == nil {
		t.Fatal("producer artifact was not reusable")
	}
	for _, mode := range []string{"checksum", "duplicate", "unknown", "job", "schema", "advisory", "time", "candidate", "context", "version", "missing", "noncanonical", "null", "deep", "many-numbers", "long-number", "bad-utf8", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			c := record
			switch mode {
			case "checksum":
				c.Artifact = strings.Replace(raw, `"lane":"consider"`, `"lane":"apply"`, 1)
			case "duplicate":
				c.Artifact = `{"job_id":"hidden",` + raw[1:]
			case "unknown":
				c.Artifact = `{"unrecognized":true,` + raw[1:]
			case "job":
				c.Artifact = strings.Replace(raw, `"job_id":"one"`, `"job_id":"other"`, 1)
			case "schema":
				c.Artifact = strings.Replace(raw, `"schema_version":2`, `"schema_version":1`, 1)
			case "advisory":
				c.Artifact = strings.Replace(raw, "advisory_only", "automatic_submit", 1)
			case "time":
				c.ComputedAt = "2026-09-10T12:00:00Z"
			case "candidate":
				c.CandidateRevision = "sha256:" + strings.Repeat("b", 64)
			case "context":
				c.ContextRevision = "sha256:" + strings.Repeat("c", 64)
			case "version":
				c.AssessmentVersion++
			case "missing":
				c.Artifact = strings.Replace(raw, `"lane":"consider",`, "", 1)
			case "noncanonical":
				c.Artifact = " " + raw
			case "null":
				c.Artifact = "null"
			case "deep":
				c.Artifact = strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40)
			case "many-numbers":
				c.Artifact = "[" + strings.Repeat("0,", 10001) + "0]"
			case "long-number":
				c.Artifact = "[" + strings.Repeat("1", 4097) + "]"
			case "bad-utf8":
				c.Artifact = string([]byte{0xff})
			case "oversize":
				c.Artifact = strings.Repeat(" ", maxArtifactBytes+1)
			}
			if p.restore(c) != nil {
				t.Fatal("unqualified artifact reused")
			}
		})
	}
	if boundedArtifact([]byte("[" + strings.Repeat("0,", 10001) + "0]")) {
		t.Fatal("scalar nodes not bounded")
	}
}
func TestSavedRequestAndFindingScope(t *testing.T) {
	base := Request{Operation: "load", Fields: map[string]any{"job_id": "one"}, Month: assessmentcontext.Month{Year: 2026, Number: 9}}
	p, e := request(base)
	if e != nil || !p.register {
		t.Fatal("load defaults to registration")
	}
	for _, id := range []any{nil, true, 1, "", "../one", ".", "..", "one/child", "one:ads", " one ", "one\x00"} {
		r := base
		r.Fields = map[string]any{"job_id": id}
		if _, e := request(r); e == nil {
			t.Fatal("bad id accepted")
		}
	}
	r := base
	r.Fields = map[string]any{"job_id": "one", "register_application": "true"}
	if _, e := request(r); e == nil {
		t.Fatal("bool coercion")
	}
	r = base
	r.Operation = "resolve"
	r.Fields = map[string]any{"job_id": "one", "finding_key": "visa", "resolution": "confirmed"}
	if _, e := request(r); e == nil {
		t.Fatal("resolution without reviewed revision")
	}
	_, a := cacheFixture(t)
	for _, key := range []string{"recommendation", "visa", "restrictions", "seniority", "location", "experience:none", "requirements:none"} {
		if !findingExists(a, key) {
			t.Fatal(key)
		}
	}
	a.ExperienceReview = []jobevaluation.ExperienceReview{{MinimumYears: json.Number("10")}}
	a.RequirementGaps = []jobevaluation.RequirementGap{{Requirement: "Python"}}
	a.ClassifiedRequirementReview = []jobevaluation.ClassifiedReview{{Requirement: "required", Status: "manual_review"}, {Requirement: "preferred", Status: "manual_review"}}
	for _, key := range []string{"experience:0", "requirement:0", "requirement:1"} {
		if !findingExists(a, key) {
			t.Fatal(key)
		}
	}
	for _, key := range []string{"experience:none", "requirements:none", "experience:1", "experience:00", "requirement:2", "requirement:-1", "arbitrary"} {
		if findingExists(a, key) {
			t.Fatal(key)
		}
	}
	for _, value := range []any{p, base, fail("PRIVATE_NATIVE_CAUSE")} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, "PRIVATE") || strings.Contains(text, "one") {
				t.Fatal("format leaked")
			}
		}
	}
	if _, e := Prepare(nil, nil, nil, base); e == nil {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Prepare(ctx, nil, nil, base); e == nil {
		t.Fatal("canceled input")
	}
}
