//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
	"careerhub.local/backend/internal/runtimepaths"
)

const reanalysisTestHome = "CAREERHUB_TEST_REANALYSIS_HOME"
const reanalysisTestResources = "CAREERHUB_TEST_REANALYSIS_RESOURCES"
const reanalysisTestSource = "Company: Fresh Labs\nRole: Backend Engineer\nResponsibilities\nBuild services.\nRequirements\nPython experience required.\n"

func TestJobReanalysisOwnedProcess(t *testing.T) {
	if home := os.Getenv(reanalysisTestHome); home != "" {
		runJobReanalysisOwned(t, home, os.Getenv(reanalysisTestResources))
		return
	}
	base := t.TempDir()
	home, resources := filepath.Join(base, "home"), filepath.Join(base, "resources")
	fixtureWrite(t, home, datahome.MarkerPath, []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`))
	fixtureWrite(t, home, "jobs/incoming/synthetic.txt", []byte(reanalysisTestSource))
	fixtureWrite(t, home, "career_vault/visa_profile.yaml", []byte("current_status: citizen\nrequires_sponsorship_now: false\n"))
	fixtureWrite(t, home, catalogstore.CustomGroupsPath, []byte("[]\n"))
	fixtureWrite(t, home, catalogstore.CustomSkillsPath, []byte("[]\n"))
	fixtureWrite(t, resources, catalogstore.CoreGroupsPath, []byte("[]\n"))
	fixtureWrite(t, resources, catalogstore.CoreSkillsPath, []byte("- {id: python, name: Python, category: programming_language}\n"))
	fixtureWrite(t, home, "jobs/normalized/synthetic.yaml", []byte(`id: synthetic
status: draft
company: Old company
title: User title
source_url: https://example.test/jobs/1
location: ''
remote_policy: ''
seniority: ''
required_skills: [ManualSkill]
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: []
visa_signals: {citizenship_required: null, clearance_required: null, sponsorship_mentioned: null, no_visa_or_opt: null}
raw_jd_path: jobs/incoming/synthetic.txt
notes: [Keep this note]
intake_idempotency_key: synthetic-key-123
manual_overrides: [title, required_skills]
digest:
  schema_version: 1
  analyzer_version: 11
  fields:
    company: {value: Old company, state: derived, evidence: []}
    title: {value: User title, state: provided, evidence: []}
    location: {value: '', state: not_stated, evidence: []}
    remote_policy: {value: '', state: not_stated, evidence: []}
    seniority: {value: '', state: not_stated, evidence: []}
  unresolved_fields: []
  eligibility_gates: {}
`))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJobReanalysisOwnedProcess$")
	cmd.Env = append(os.Environ(), reanalysisTestHome+"="+home, reanalysisTestResources+"="+resources)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned rescan failed: %v, deadline=%v, output=%.1000s", err, ctx.Err(), output)
	}

}

func fixtureWrite(t *testing.T, root, relative string, raw []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func reanalysisCardField(card cataloginput.Value, path ...string) cataloginput.Value {
	for _, name := range path {
		if card.Kind != "mapping" {
			return cataloginput.Value{}
		}
		found := false
		for _, entry := range card.Entries {
			if entry.Key.Kind == "string" && entry.Key.Text == name {
				card, found = entry.Value, true
				break
			}
		}
		if !found {
			return cataloginput.Value{}
		}
	}
	return card
}

func reanalysisPreparedFixture(t *testing.T, home, resources string) (ReanalysisPreparation, string) {
	t.Helper()
	ctx := context.Background()
	dataRoot, err := runtimepaths.Open(home)
	if err != nil {
		t.Fatal(err.Code)
	}
	defer dataRoot.Close()
	resourceRoot, err := runtimepaths.Open(resources)
	if err != nil {
		t.Fatal(err.Code)
	}
	defer resourceRoot.Close()
	card, present, cardErr := dataRoot.ReadSingleLinkFile("jobs/normalized/synthetic.yaml", cataloginput.YAMLMaxInputBytes)
	if cardErr != nil || !present {
		t.Fatal("card fixture read")
	}
	raw, present, rawErr := dataRoot.ReadSingleLinkFile("jobs/incoming/synthetic.txt", jobdigest.MaxSourceBytes)
	if rawErr != nil || !present {
		t.Fatal("raw fixture read")
	}
	profile, profilePresent, profileErr := dataRoot.ReadSingleLinkFile("career_vault/visa_profile.yaml", cataloginput.YAMLMaxInputBytes)
	if profileErr != nil || !profilePresent {
		t.Fatal("profile fixture read")
	}
	value, parseErr := cataloginput.ParseYAML(profile)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	projection, projectionErr := jobdigest.ProfileProjection(ctx, value)
	if projectionErr != nil {
		t.Fatal(projectionErr)
	}
	registry, registryErr := catalogstore.ReadState(resourceRoot, dataRoot)
	if registryErr != nil {
		t.Fatal(registryErr.Code)
	}
	result, digestErr := jobdigest.Analyze(ctx, jobdigest.Input{Source: string(raw), Title: "User title", SourceURL: "https://example.test/jobs/1", Profile: projection, Registry: registry.Snapshot})
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	cardHash := sha256.Sum256(card)
	loaded, loadedErr := jobcard.Read(ctx, dataRoot, "jobs/normalized/synthetic.yaml")
	if loadedErr != nil {
		t.Fatal(loadedErr.Code)
	}
	plannedHash := cardHash
	if reanalysisCardField(loaded.Card, "status").Text == "draft" {
		plannedCard, transformErr := jobcard.PrepareReanalysis(ctx, loaded, result)
		if transformErr != nil {
			t.Fatal(transformErr.Code)
		}
		plannedBytes, encodeErr := jobcard.EncodeCard(ctx, plannedCard)
		if encodeErr != nil {
			t.Fatal(encodeErr.Code)
		}
		plannedHash = sha256.Sum256(plannedBytes)
	}
	return ReanalysisPreparation{Digest: result, CardFileSHA256: cardHash, RawFileSHA256: sha256.Sum256(raw),
			ProfileFileSHA256: sha256.Sum256(profile), ProfilePresent: profilePresent, CatalogRevision: registry.Snapshot.CatalogRevision(), PlannedCardSHA256: plannedHash},
		"sha256:" + hex.EncodeToString(cardHash[:])
}

func runJobReanalysisOwned(t *testing.T, home, resources string) {
	t.Helper()
	root, openErr := runtimepaths.Open(home)
	if openErr != nil {
		t.Fatal(openErr.Code)
	}
	defer root.Close()
	info, inspectErr := datahome.Inspect(home, nil)
	if inspectErr != nil {
		t.Fatal(inspectErr.Code)
	}
	owner := New(home).PinnedToHome(*info).PinnedToResources(resources)
	prepared, revision := reanalysisPreparedFixture(t, home, resources)
	if out, err := owner.ReanalyzeJob(context.Background(), "synthetic", revision, prepared); err == nil || err.Code != "DATA_HOME_OWNERSHIP_REQUIRED" || out.CommitAttempted {
		t.Fatalf("unowned rescan accepted: %#v %#v", out, err)
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e.Code)
	}
	if out, err := owner.ReanalyzeJob(context.Background(), "synthetic", "sha256:"+strings.Repeat("0", 64), prepared); err == nil || err.Code != "JOB_REANALYSIS_REVISION_CONFLICT" || out.CommitAttempted {
		t.Fatalf("stale revision accepted: %#v %#v", out, err)
	}
	cardPath := filepath.Join(home, "jobs", "normalized", "synthetic.yaml")
	before, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		root, path string
		changed    []byte
	}{
		{home, "jobs/incoming/synthetic.txt", []byte("Changed raw JD.\n")},
		{home, "career_vault/visa_profile.yaml", []byte("current_status: permanent_resident\n")},
		{resources, catalogstore.CoreSkillsPath, []byte("- {id: python, name: Python, category: programming_language}\n- {id: java, name: Java, category: programming_language}\n")},
	} {
		full := filepath.Join(test.root, filepath.FromSlash(test.path))
		original, readErr := os.ReadFile(full)
		missing := os.IsNotExist(readErr)
		if readErr != nil && !missing {
			t.Fatal(readErr)
		}
		fixtureWrite(t, test.root, test.path, test.changed)
		out, problem := owner.ReanalyzeJob(context.Background(), "synthetic", revision, prepared)
		if problem == nil || problem.Code != "JOB_REANALYSIS_INPUT_CHANGED" || out.CommitAttempted || out.Committed {
			t.Fatalf("changed input accepted: %s %#v %#v", test.path, out, problem)
		}
		if missing {
			if e := os.Remove(full); e != nil {
				t.Fatal(e)
			}
		} else if e := os.WriteFile(full, original, 0600); e != nil {
			t.Fatal(e)
		}
		current, readErr := os.ReadFile(cardPath)
		if readErr != nil || !bytes.Equal(current, before) {
			t.Fatal("refused rescan changed card")
		}
	}
	wrongPlan := prepared
	wrongPlan.PlannedCardSHA256 = sha256.Sum256([]byte("different planned card"))
	if out, err := owner.ReanalyzeJob(context.Background(), "synthetic", revision, wrongPlan); err == nil || err.Code != "JOB_REANALYSIS_PLAN_CHANGED" || out.CommitAttempted || out.Committed {
		t.Fatalf("unplanned card publication accepted: %#v %#v", out, err)
	}
	if current, readErr := os.ReadFile(cardPath); readErr != nil || !bytes.Equal(current, before) {
		t.Fatal("plan mismatch changed card")
	}
	out, problem := owner.ReanalyzeJob(context.Background(), "synthetic", revision, prepared)
	if problem != nil || !out.Committed || !out.CommitAttempted || len(out.Result) == 0 {
		t.Fatalf("one-card rescan failed: %#v %#v", out, problem)
	}
	changed, readErr := os.ReadFile(cardPath)
	if readErr != nil || bytes.Equal(before, changed) {
		t.Fatal("rescan did not replace card")
	}
	loaded, cardErr := jobcard.Read(context.Background(), root, "jobs/normalized/synthetic.yaml")
	if cardErr != nil || loaded == nil || !loaded.Report.SchemaValid() {
		t.Fatalf("stored rescan card invalid: %#v", cardErr)
	}
	key, note := bytes.Contains(changed, []byte("synthetic-key-123")), bytes.Contains(changed, []byte("Keep this note"))
	skill, title := bytes.Contains(changed, []byte("ManualSkill")), bytes.Contains(changed, []byte("User title"))
	v12 := reanalysisCardField(loaded.Card, "digest", "analyzer_version").Text == "12"
	company := reanalysisCardField(loaded.Card, "company").Text == "Fresh Labs"
	if !key || !note || !skill || !title || !v12 || !company {
		t.Fatalf("rescan fields: retry=%t note=%t skill=%t title=%t v12=%t company=%t", key, note, skill, title, v12, company)
	}
	retainedSource, sourceErr := os.ReadFile(filepath.Join(home, "jobs", "incoming", "synthetic.txt"))
	if sourceErr != nil || string(retainedSource) != reanalysisTestSource {
		t.Fatal("rescan changed the retained source")
	}
	if out, err := owner.ReanalyzeJob(context.Background(), "synthetic", revision, prepared); err == nil || err.Code != "JOB_REANALYSIS_REVISION_CONFLICT" || out.CommitAttempted {
		t.Fatalf("stale retry accepted: %#v %#v", out, err)
	}
	prepared, revision = reanalysisPreparedFixture(t, home, resources)
	out, problem = owner.ReanalyzeJob(context.Background(), "synthetic", revision, prepared)
	if problem != nil || out.CommitAttempted || out.Committed || !bytes.Contains(out.Result, []byte(`"unchanged":true`)) {
		t.Fatalf("already-current card was rewritten: %#v %#v", out, problem)
	}
	if current, readErr := os.ReadFile(cardPath); readErr != nil || !bytes.Equal(current, changed) {
		t.Fatal("already-current rescan changed card bytes")
	}
	fixtureWrite(t, home, "jobs/incoming/synthetic.txt", []byte(reanalysisTestSource+"Additional duty: keep records.\n"))
	prepared, revision = reanalysisPreparedFixture(t, home, resources)
	faulty := owner.PinnedToHome(*info)
	faulty.checkpoint = func(stage string) *Error {
		if stage == "job_reanalysis_committed" {
			return failed("synthetic", "JOB_REANALYSIS_POSTCOMMIT_TEST")
		}
		return nil
	}
	out, problem = faulty.ReanalyzeJob(context.Background(), "synthetic", revision, prepared)
	if problem == nil || problem.Code != "JOB_REANALYSIS_POSTCOMMIT_TEST" || !out.Committed || !out.CommitAttempted || len(out.Result) != 0 {
		t.Fatalf("postcommit fact lost: %#v %#v", out, problem)
	}
	fixtureWrite(t, home, "jobs/incoming/synthetic.txt", []byte(reanalysisTestSource+"Additional duty: keep records.\nAdditional duty: verify records.\n"))
	prepared, revision = reanalysisPreparedFixture(t, home, resources)
	cleanupFault := owner.PinnedToHome(*info)
	cleanupFault.closeRoot = func(r *runtimepaths.Root) error {
		_ = r.Close()
		return &runtimepaths.Error{Code: "SYNTHETIC_CLOSE"}
	}
	out, problem = cleanupFault.ReanalyzeJob(context.Background(), "synthetic", revision, prepared)
	if problem == nil || !out.Committed || !out.CleanupFailed || len(out.Result) != 0 {
		t.Fatalf("cleanup fault hid a committed card: %#v %#v", out, problem)
	}
	current, readErr := os.ReadFile(cardPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	readyCard, parseErr := cataloginput.ParseYAML(current)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	for index := range readyCard.Entries {
		if readyCard.Entries[index].Key.Text == "status" {
			readyCard.Entries[index].Value = cataloginput.Value{Kind: "string", Text: "ready"}
		}
	}
	readyBytes, encodeErr := jobcard.EncodeCard(context.Background(), readyCard)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if e := os.WriteFile(cardPath, readyBytes, 0600); e != nil {
		t.Fatal(e)
	}
	prepared, revision = reanalysisPreparedFixture(t, home, resources)
	if out, err := owner.ReanalyzeJob(context.Background(), "synthetic", revision, prepared); err == nil || err.Code != "JOB_REANALYSIS_NOT_DRAFT" || out.CommitAttempted {
		t.Fatalf("ready card was rescanned: %#v %#v", out, err)
	}
}
