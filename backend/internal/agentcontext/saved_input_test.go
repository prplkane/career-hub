package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/runtimepaths"
)

type savedSourceFixture struct {
	files  map[string]string
	reads  map[string]int
	before func(string, int)
	deny   string
}

func (s *savedSourceFixture) Check(path string) *runtimepaths.Error {
	if path == s.deny {
		return &runtimepaths.Error{Code: "PRIVATE_NATIVE_CAUSE"}
	}
	return nil
}
func (s *savedSourceFixture) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	s.reads[path]++
	if s.before != nil {
		s.before(path, s.reads[path])
	}
	raw, ok := s.files[path]
	if int64(len(raw)) > limit {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	return []byte(raw), ok, nil
}
func savedInputFixture() (*savedSourceFixture, *savedSourceFixture) {
	d := &savedSourceFixture{reads: map[string]int{}, files: map[string]string{
		"jobs/normalized/synthetic.yaml": `id: synthetic
status: ready
company: Synthetic
title: Engineer
source_url: https://example.test/job
location: ''
remote_policy: ''
seniority: ''
required_skills: [Python]
required_platforms: []
required_capabilities: []
preferred_skills: [SQL]
experience_requirements: []
education_requirements: []
responsibilities: [Build services.]
visa_signals: {citizenship_required: null, clearance_required: null, sponsorship_mentioned: null, no_visa_or_opt: null}
raw_jd_path: jobs/incoming/synthetic.txt
`,
		"jobs/incoming/synthetic.txt":         "Engineer\r\nPython required. SQL preferred.\r\n",
		"career_vault/profile.yaml":           "name: Synthetic\ntarget_location: US\nwork_authorization: synthetic\nmajor: Computer Science\ncontact: {email: PRIVATE@example.test}\n",
		"career_vault/education.yaml":         "schools: [{id: school, institution: Synthetic College, degree: BS}]\ncertifications: [{id: cert, name: Synthetic, credential: Test, completed: '2026', proof_points: []}]\n",
		"career_vault/visa_profile.yaml":      "current_status: synthetic\nwork_authorization: synthetic\nrequires_future_sponsorship: false\nstem_opt_eligible: false\n",
		"career_vault/experience_blocks.yaml": "- {id: exp, company: Synthetic, role: Engineer, dates: '2025', category: technical, technologies: [Python], proof_points: ['Built Python services.']}\n",
		"career_vault/project_blocks.yaml":    "- {id: project, name: Synthetic, category: technical, summary: Test, technologies: [SQL], proof_points: [], related_experience: [exp]}\n",
		"career_vault/skill_blocks.yaml":      "- {id: skill, skill_family: Technical, related_experience: [exp], related_projects: [project], related_certifications: [cert], keywords: []}\n",
		"career_vault/proof_blocks.yaml":      "- {id: proof, claim: Synthetic, evidence_type: experience, source_block: exp}\n",
		"career_vault/style_rules.yaml":       "standards_metadata: {reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [false]}\nresume_voice: {}\nbullet_rules: {}\ndocument_rules: {layout: single_column, standard_sections: [false]}\nats_rules: {}\nselection_rules: {allowed_decisions: [full, compact, excluded]}\nquality_gates: {}\nforbidden: []\n",
	}}
	return d, &savedSourceFixture{files: map[string]string{}, reads: map[string]int{}}
}

var savedInputDate = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func savedEvidencePacket(t *testing.T, evidence *SavedEvidence, purpose string) []byte {
	t.Helper()
	payload, e := BuildPacket(context.Background(), PacketInput{
		Job: evidence.Job, Assessment: evidence.Assessment, Sources: evidence.Sources,
		RegistryGroups: evidence.RegistryGroups, Profile: "sandbox", Channel: "preview",
		Purpose: purpose, Operation: "context", RegistryMode: "off", Persisted: true,
		WriteReceipt: json.RawMessage(`{"result":"read_only","changed":false}`),
	}, nil)
	if e != nil {
		t.Fatalf("saved evidence does not compose: %s", e.Code)
	}
	return payload
}

func TestSavedEvidenceComposesFreshPacket(t *testing.T) {
	d, r := savedInputFixture()
	got, e := ReadSavedEvidence(context.Background(), d, r, "synthetic", savedInputDate)
	if e != nil {
		t.Fatalf("saved evidence: %s", e.Code)
	}
	for _, source := range []*savedSourceFixture{d, r} {
		for path, count := range source.reads {
			if count != 2 {
				t.Fatalf("not one load and one recheck: %s = %d", path, count)
			}
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", got), "Synthetic") {
		t.Fatal("evidence diagnostics leaked content")
	}
	if !bytes.Contains(got.Job, []byte(`"id":"synthetic","status":"ready","company":"Synthetic"`)) {
		t.Fatal("source mapping order changed")
	}
	for _, purpose := range []string{"assessment", "resume_delta", "application_text"} {
		payload := savedEvidencePacket(t, got, purpose)
		if bytes.Contains(payload, []byte("PRIVATE@example.test")) {
			t.Fatal("private contact entered agent packet")
		}
		if !bytes.Contains(payload, []byte(`"exp"`)) {
			t.Fatal("direct evidence missing")
		}
	}
	d.files["career_vault/experience_blocks.yaml"] = "[]"
	if !bytes.Contains(got.Sources.Experiences, []byte("Built Python services.")) {
		t.Fatal("output aliases source storage")
	}
}

func TestSavedEvidenceRefusesInvalidOrChangingSources(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*savedSourceFixture, *savedSourceFixture)
	}{
		{"missing-card", func(d, r *savedSourceFixture) { delete(d.files, "jobs/normalized/synthetic.yaml") }},
		{"wrong-card-id", func(d, r *savedSourceFixture) {
			d.files["jobs/normalized/synthetic.yaml"] = strings.Replace(d.files["jobs/normalized/synthetic.yaml"], "id: synthetic", "id: different", 1)
		}},
		{"unrelated-invalid-vault", func(d, r *savedSourceFixture) { delete(d.files, "career_vault/proof_blocks.yaml") }},
		{"invalid-policy", func(d, r *savedSourceFixture) { d.files["career_vault/hard_blocker_policy.yaml"] = "{}" }},
		{"unsupported-ordered-value", func(d, r *savedSourceFixture) { d.files["career_vault/profile.yaml"] += "extra: 2026-01-01\n" }},
		{"read-denied", func(d, r *savedSourceFixture) { d.deny = "career_vault/profile.yaml" }},
		{"vault-changed", func(d, r *savedSourceFixture) {
			d.before = func(path string, n int) {
				if path == "career_vault/profile.yaml" && n == 2 {
					d.files[path] += "extra: changed\n"
				}
			}
		}},
		{"source-changed", func(d, r *savedSourceFixture) {
			d.before = func(path string, n int) {
				if path == "jobs/incoming/synthetic.txt" && n == 2 {
					d.files[path] += "changed"
				}
			}
		}},
		{"missing-policy-created", func(d, r *savedSourceFixture) {
			d.before = func(path string, n int) {
				if path == "career_vault/hard_blocker_policy.yaml" && n == 2 {
					d.files[path] = "{}"
				}
			}
		}},
		{"resource-created", func(d, r *savedSourceFixture) {
			r.before = func(path string, n int) {
				if path == "src/data/requirement_concepts.yaml" && n == 2 {
					r.files[path] = "{}"
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, r := savedInputFixture()
			test.edit(d, r)
			got, e := ReadSavedEvidence(context.Background(), d, r, "synthetic", savedInputDate)
			if got != nil || e == nil {
				t.Fatal("invalid snapshot returned partial evidence")
			}
			if strings.Contains(e.Code, "PRIVATE") || strings.Contains(fmt.Sprintf("%+v", e), "PRIVATE") {
				t.Fatal("native failure leaked")
			}
		})
	}
	for _, id := range []string{"", "..", "../synthetic", "a/b", "a\\b", "a:b", "\xff", strings.Repeat("x", 257)} {
		d, r := savedInputFixture()
		if got, e := ReadSavedEvidence(context.Background(), d, r, id, savedInputDate); got != nil || e == nil || len(d.reads) != 0 || len(r.reads) != 0 {
			t.Fatal("invalid ID read data")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d, r := savedInputFixture()
	if got, e := ReadSavedEvidence(ctx, d, r, "synthetic", savedInputDate); got != nil || e == nil || len(d.reads) != 0 {
		t.Fatal("canceled input read data")
	}
	ctx, cancel = context.WithCancel(context.Background())
	r.before = func(path string, n int) {
		if n == 2 {
			cancel()
		}
	}
	if got, e := ReadSavedEvidence(ctx, d, r, "synthetic", savedInputDate); got != nil || e == nil {
		t.Fatal("late cancellation returned data")
	}
}

func TestObservedEvidenceSourceBudgetsAndIsolation(t *testing.T) {
	f := &savedSourceFixture{files: map[string]string{"one": "abcdef", "two": "xyz"}, reads: map[string]int{}}
	remaining := 8
	s := &observedEvidenceSource{ctx: context.Background(), source: f, files: map[string]evidenceFile{}, remaining: &remaining}
	raw, present, e := s.ReadFile("one", 6)
	if e != nil || !present || remaining != 2 {
		t.Fatal("source budget not charged")
	}
	raw[0] = 'X'
	again, _, e := s.ReadFile("one", 6)
	if e != nil || string(again) != "abcdef" || f.reads["one"] != 1 || remaining != 2 {
		t.Fatal("cached bytes mutated or charged twice")
	}
	if got, _, e := s.ReadFile("one", 5); got != nil || e == nil {
		t.Fatal("cached content ignored tighter limit")
	}
	other := &observedEvidenceSource{ctx: context.Background(), source: f, files: map[string]evidenceFile{}, remaining: &remaining}
	if got, _, e := other.ReadFile("two", 3); got != nil || e == nil || len(other.files) != 0 {
		t.Fatal("combined source budget bypassed")
	}
	for _, limit := range []int64{-1, 0, (4 << 20) + 1} {
		if got, _, e := s.ReadFile("one", limit); got != nil || e == nil {
			t.Fatal("unbounded source read accepted")
		}
	}
	for i := 0; i < 31; i++ {
		if _, _, e := s.ReadFile(fmt.Sprintf("absent-%d", i), 8); e != nil {
			t.Fatal("bounded absent observation rejected")
		}
	}
	if _, _, e := s.ReadFile("one-more", 8); e == nil {
		t.Fatal("file observation limit bypassed")
	}
	if e := s.recheck(); e != nil {
		t.Fatal("stable observations failed recheck")
	}
	delete(f.files, "one")
	if e := s.recheck(); e == nil || e.Code != "AGENT_CONTEXT_SOURCE_CHANGED" {
		t.Fatal("source disappearance ignored")
	}
}
