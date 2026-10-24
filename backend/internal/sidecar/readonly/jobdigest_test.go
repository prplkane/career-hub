package readonly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/matching"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const digestCardPath = "jobs/normalized/digest.yaml"
const digestRawPath = "jobs/incoming/digest.txt"
const digestProfilePath = "career_vault/visa_profile.yaml"
const digestCard = `id: digest
status: draft
company: Previously derived company
title: Previously derived title
source_url: https://example.test/job/1
location: ''
remote_policy: ''
seniority: ''
required_skills: []
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: []
visa_signals: {citizenship_required: null, clearance_required: null, sponsorship_mentioned: null, no_visa_or_opt: null}
raw_jd_path: jobs/incoming/digest.txt
`
const digestSource = "\ufeffCompany: Source Company\r\nRole: Data Engineer\r\nRequirements\r\nPython and LocalTool.\r\nNo visa sponsorship.\r\n"

type digestMemory struct {
	files  map[string][]byte
	steps  []string
	before func(string, string) *runtimepaths.Error
	after  func(string, string)
}

func (m *digestMemory) Check(path string) *runtimepaths.Error {
	m.steps = append(m.steps, "check:"+path)
	if m.before != nil {
		if err := m.before("check", path); err != nil {
			return err
		}
	}
	if m.after != nil {
		m.after("check", path)
	}
	return nil
}
func (m *digestMemory) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if limit != cataloginput.YAMLMaxInputBytes && limit != applicationanalysis.MaxSourceBytes {
		panic("unbounded digest read")
	}
	m.steps = append(m.steps, "read:"+path)
	if m.before != nil {
		if err := m.before("read", path); err != nil {
			return nil, false, err
		}
	}
	raw, present := m.files[path]
	raw = bytes.Clone(raw)
	if m.after != nil {
		m.after("read", path)
	}
	return raw, present, nil
}
func digestMemories() (*digestMemory, *digestMemory) {
	data := &digestMemory{files: map[string][]byte{
		digestCardPath: []byte(digestCard), digestRawPath: []byte(digestSource),
		digestProfilePath:             []byte("current_status: citizen\nrequires_sponsorship_now: false\n"),
		catalogstore.CustomGroupsPath: []byte("- {id: custom_tools, name: My tools}\n"),
		catalogstore.CustomSkillsPath: []byte("- {id: local_tool, name: LocalTool, category: custom_skill, primary_group_id: custom_tools}\n"),
	}}
	resources := &digestMemory{files: map[string][]byte{
		catalogstore.CoreGroupsPath: []byte("[]"),
		catalogstore.CoreSkillsPath: []byte("- {id: python, name: Python, category: programming_language}\n"),
	}}
	return data, resources
}
func TestRetainedJobDigestFixedReadSetAndFreshness(t *testing.T) {
	data, resources := digestMemories()
	beforeData, beforeResources := map[string][]byte{}, map[string][]byte{}
	for k, v := range data.files {
		beforeData[k] = bytes.Clone(v)
	}
	for k, v := range resources.files {
		beforeResources[k] = bytes.Clone(v)
	}
	got, err := digestJobSource(context.Background(), data, resources, digestCardPath, DigestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest.Fields["company"].Value != "Source Company" || got.Digest.Fields["company"].State != "explicit" || got.Digest.Fields["title"].Value != "Data Engineer" || len(got.Digest.Occurrences) != 2 || got.CatalogRevision == "" {
		t.Fatal("fresh source was replaced by saved derived hints")
	}
	if !got.ProfilePresent || got.ProfileFileSHA256 != sha256.Sum256(beforeData[digestProfilePath]) {
		t.Fatal("profile witness does not identify analyzed bytes")
	}
	if !reflect.DeepEqual(beforeData, data.files) || !reflect.DeepEqual(beforeResources, resources.files) {
		t.Fatal("read mutated source")
	}
	for _, step := range data.steps {
		_, path, _ := strings.Cut(step, ":")
		if _, ok := beforeData[path]; !ok {
			t.Fatalf("unplanned private file read: %s", path)
		}
	}
	for _, step := range resources.steps {
		_, path, _ := strings.Cut(step, ":")
		if _, ok := beforeResources[path]; !ok {
			t.Fatal("unplanned resource read")
		}
	}
	data.files[digestRawPath] = []byte(strings.ReplaceAll(digestSource, "Python", "Java"))
	fresh, err := digestJobSource(context.Background(), data, resources, digestCardPath, DigestOptions{Title: "Explicit title", Company: "Explicit company"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RawFileSHA256 == got.RawFileSHA256 || fresh.Digest.Fields["title"].State != "provided" || fresh.Digest.Fields["title"].Value != "Explicit title" || fresh.Digest.Fields["company"].Value != "Explicit company" || len(fresh.Digest.Occurrences) != 1 {
		t.Fatal("stale result or lost explicit override")
	}
	resources.files[catalogstore.CoreSkillsPath] = []byte("- {id: java, name: Java, category: programming_language}\n")
	next, err := digestJobSource(context.Background(), data, resources, digestCardPath, DigestOptions{})
	if err != nil || next.CatalogRevision == fresh.CatalogRevision || len(next.Digest.Occurrences) != 2 {
		t.Fatal("core catalog refresh")
	}
	data.files[digestProfilePath] = []byte("current_status: permanent_resident\nrequires_sponsorship_now: false\n")
	changedProfile, err := digestJobSource(context.Background(), data, resources, digestCardPath, DigestOptions{})
	if err != nil || !changedProfile.ProfilePresent || changedProfile.ProfileFileSHA256 == next.ProfileFileSHA256 || changedProfile.CardFileSHA256 != next.CardFileSHA256 || changedProfile.RawFileSHA256 != next.RawFileSHA256 || changedProfile.CatalogRevision != next.CatalogRevision {
		t.Fatal("profile-only edit did not change only its witness")
	}
	delete(data.files, digestProfilePath)
	absentProfile, err := digestJobSource(context.Background(), data, resources, digestCardPath, DigestOptions{})
	if err != nil || absentProfile.ProfilePresent {
		t.Fatal("absent profile witness")
	}
	data.files[digestProfilePath] = []byte{}
	emptyProfile, err := digestJobSource(context.Background(), data, resources, digestCardPath, DigestOptions{})
	if err != nil || !emptyProfile.ProfilePresent || emptyProfile.ProfileFileSHA256 != absentProfile.ProfileFileSHA256 {
		t.Fatal("empty profile must differ from absent by presence")
	}
}

func TestRetainedJobDigestReanalysisUsesOnlyPreviouslyProvidedHints(t *testing.T) {
	data, resources := digestMemories()
	data.files[digestCardPath] = []byte(strings.Replace(digestCard, "company: Previously derived company", "company: User supplied company", 1) + `digest:
  schema_version: 1
  fields:
    company: {value: User supplied company, state: provided, evidence: []}
    title: {value: Previously derived title, state: derived, evidence: []}
    location: {value: '', state: not_stated, evidence: []}
    remote_policy: {value: '', state: not_stated, evidence: []}
    seniority: {value: '', state: not_stated, evidence: []}
  unresolved_fields: []
  eligibility_gates: {}
`)
	got, err := digestJobSource(context.Background(), data, resources, digestCardPath, DigestOptions{Reanalysis: true})
	if err != nil || got.Digest.Fields["company"].State != "provided" || got.Digest.Fields["company"].Value != "User supplied company" || got.Digest.Fields["title"].State != "explicit" || got.Digest.Fields["title"].Value != "Data Engineer" {
		t.Fatalf("rescan hints did not follow the saved state: %#v %#v", got, err)
	}
	loaded, readErr := jobcard.Read(context.Background(), data, digestCardPath)
	if readErr != nil {
		t.Fatal(readErr.Code)
	}
	card, transformErr := jobcard.PrepareReanalysis(context.Background(), loaded, got.Digest)
	if transformErr != nil {
		t.Fatal(transformErr.Code)
	}
	encoded, encodeErr := jobcard.EncodeCard(context.Background(), card)
	if encodeErr != nil || got.PlannedCardSHA256 != sha256.Sum256(encoded) || got.PlannedCardSHA256 == got.CardFileSHA256 {
		t.Fatal("planned hash does not identify the final unpublished card")
	}
	if value, err := digestJobSource(context.Background(), data, resources, digestCardPath, DigestOptions{Reanalysis: true, Company: "unseen hint"}); err == nil || value != nil {
		t.Fatal("reanalysis accepted an unrelated caller-provided hint")
	}
}

func TestRetainedJobDigestInvalidInputs(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(*digestMemory, *digestMemory)
	}{
		{"missing-card", "JOB_CARD_NOT_FOUND", func(d, r *digestMemory) { delete(d.files, digestCardPath) }},
		{"missing-source", "JOB_DIGEST_CARD_INVALID", func(d, r *digestMemory) { delete(d.files, digestRawPath) }},
		{"empty-source", "JOB_DIGEST_SOURCE_MISSING", func(d, r *digestMemory) { d.files[digestRawPath] = []byte("\ufeff \r\n") }},
		{"private-path", "JOB_DIGEST_CARD_INVALID", func(d, r *digestMemory) {
			d.files[digestCardPath] = []byte(strings.ReplaceAll(digestCard, digestRawPath, "documents/private.bin"))
			d.files["documents/private.bin"] = []byte("NEVER_READ")
		}},
		{"invalid-schema", "JOB_DIGEST_CARD_INVALID", func(d, r *digestMemory) { d.files[digestCardPath] = []byte("id: 123") }},
		{"invalid-utf8", "JOB_CARD_SOURCE_ENCODING", func(d, r *digestMemory) { d.files[digestRawPath] = []byte{255} }},
		{"invalid-profile-yaml", "JOB_DIGEST_PROFILE_INVALID", func(d, r *digestMemory) { d.files[digestProfilePath] = []byte("[") }},
		{"invalid-profile-type", "JOB_DIGEST_PROFILE_INVALID", func(d, r *digestMemory) { d.files[digestProfilePath] = []byte("requires_sponsorship_now: 'yes'") }},
		{"invalid-catalog", "JOB_DIGEST_CATALOG_INVALID", func(d, r *digestMemory) { r.files[catalogstore.CoreSkillsPath] = []byte("- {id: py}") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, r := digestMemories()
			test.change(d, r)
			got, err := digestJobSource(context.Background(), d, r, digestCardPath, DigestOptions{})
			requireCode(t, err, test.code)
			if got != nil {
				t.Fatal("invalid input published partial digest")
			}
			for _, step := range d.steps {
				if strings.Contains(step, "private.bin") {
					t.Fatal("private document was accessed")
				}
			}
		})
	}
	for _, path := range []string{"../outside.yaml", "jobs/incoming/digest.txt", "C:/private.yaml"} {
		d, r := digestMemories()
		got, err := digestJobSource(context.Background(), d, r, path, DigestOptions{})
		requireCode(t, err, "JOB_CARD_PATH_INVALID")
		if got != nil || len(d.steps) != 0 || len(r.steps) != 0 {
			t.Fatal("invalid selector accessed data")
		}
	}
	for _, which := range []int{0, 1, 2} {
		d, r := digestMemories()
		var ds, rs evaluationSource = d, r
		ctx := context.Background()
		switch which {
		case 0:
			ctx = nil
		case 1:
			ds = nil
		case 2:
			rs = nil
		}
		if got, err := digestJobSource(ctx, ds, rs, digestCardPath, DigestOptions{}); err == nil || got != nil {
			t.Fatal("nil boundary")
		}
	}
	for _, workspace := range []*Workspace{nil, {}} {
		if got, err := workspace.DigestJobSource(context.Background(), digestCardPath, DigestOptions{}); err == nil || got != nil {
			t.Fatal("unadmitted workspace")
		}
	}
}

func TestRetainedJobDigestEveryIOFailureAndCancellation(t *testing.T) {
	baselineData, baselineResources := digestMemories()
	if _, err := digestJobSource(context.Background(), baselineData, baselineResources, digestCardPath, DigestOptions{}); err != nil {
		t.Fatal(err)
	}
	for role, steps := range [][]string{baselineData.steps, baselineResources.steps} {
		for at := 1; at <= len(steps); at++ {
			for _, canceling := range []bool{false, true} {
				d, r := digestMemories()
				selected := d
				if role == 1 {
					selected = r
				}
				ctx, cancel := context.WithCancel(context.Background())
				selected.before = func(operation, path string) *runtimepaths.Error {
					if len(selected.steps) == at {
						if canceling {
							cancel()
							return nil
						}
						return &runtimepaths.Error{Code: "PRIVATE_PATH_AND_SECRET"}
					}
					return nil
				}
				got, err := digestJobSource(ctx, d, r, digestCardPath, DigestOptions{})
				cancel()
				if err == nil || got != nil || strings.Contains(fmt.Sprintf("%+v", err), "PRIVATE_PATH_AND_SECRET") {
					t.Fatalf("role %d step %d cancel %v: %v", role, at, canceling, err)
				}
			}
		}
	}
	t.Logf("qualified %d private and %d resource I/O checkpoints", len(baselineData.steps), len(baselineResources.steps))
}

func TestRetainedJobDigestObservedChanges(t *testing.T) {
	for _, path := range []string{digestCardPath, digestRawPath, digestProfilePath, catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath, catalogstore.CoreGroupsPath, catalogstore.CoreSkillsPath} {
		for _, remove := range []bool{false, true} {
			d, r := digestMemories()
			selected := d
			if strings.HasPrefix(path, "src/") {
				selected = r
			}
			changed := false
			selected.after = func(op, p string) {
				if op == "read" && p == path && !changed {
					changed = true
					if remove {
						delete(selected.files, path)
					} else {
						selected.files[path] = append(bytes.Clone(selected.files[path]), []byte("\n# observed change\n")...)
					}
				}
			}
			if got, err := digestJobSource(context.Background(), d, r, digestCardPath, DigestOptions{}); err == nil || got != nil {
				t.Fatalf("observed drift %s remove=%v accepted", path, remove)
			}
		}
	}
	d, r := digestMemories()
	delete(d.files, digestProfilePath)
	seen := false
	d.after = func(op, path string) {
		if op == "read" && path == digestProfilePath && !seen {
			seen = true
			d.files[path] = []byte("requires_sponsorship_now: true")
		}
	}
	if got, err := digestJobSource(context.Background(), d, r, digestCardPath, DigestOptions{}); err == nil || got != nil {
		t.Fatal("late profile creation accepted")
	}
}

func TestDigestErrorAdapterIsStatic(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, jobdigest.ErrLimit, jobdigest.ErrInput, jobdigest.ErrSourceMismatch, matching.ErrLimit, matching.ErrInput, applicationanalysis.ErrInput, applicationanalysis.ErrLimit, errors.New("PRIVATE_SYNTHETIC_TOKEN")} {
		if mapped := digestFailure(err); mapped == nil || strings.Contains(fmt.Sprintf("%+v", mapped), "PRIVATE_SYNTHETIC_TOKEN") {
			t.Fatal("private error leaked")
		}
	}
}

type digestOracleOutput struct{ bytes.Buffer }

func (out *digestOracleOutput) Write(raw []byte) (int, error) {
	if out.Len()+len(raw) > 4<<20 {
		return 0, errors.New("digest oracle output limit")
	}
	return out.Buffer.Write(raw)
}
