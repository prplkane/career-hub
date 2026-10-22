//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

const newJobPublishHomeEnv = "CAREERHUB_TEST_NEW_JOB_PUBLISH_HOME"
const newJobPublishCaseEnv = "CAREERHUB_TEST_NEW_JOB_PUBLISH_CASE"

func frozenNewJobFixture(t *testing.T) (newJobIntent, []byte, []byte) {
	t.Helper()
	raw := []byte("Build Go services and document the production interfaces.")
	processed, sourceErr := jobcard.IntakeDigestSource(string(raw))
	if sourceErr != nil {
		t.Fatal(sourceErr.Code)
	}
	fingerprint, fingerprintErr := applicationanalysis.SourceFingerprint(processed)
	if fingerprintErr != nil {
		t.Fatal(fingerprintErr)
	}
	cardText := fmt.Sprintf(`id: synthetic
status: draft
company: Fictional
title: Software Engineer
source_url: https://example.test/jobs/1
location: ''
remote_policy: ''
seniority: ''
required_skills: [Go]
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: [Build Go services.]
visa_signals:
  citizenship_required: null
  clearance_required: null
  sponsorship_mentioned: null
  no_visa_or_opt: null
raw_jd_path: jobs/incoming/synthetic.txt
content_fingerprint: %s
`, fingerprint)
	parsed, parseErr := cataloginput.ParseYAML([]byte(cardText))
	if parseErr != nil {
		t.Fatal(parseErr.Code)
	}
	card, encodeErr := jobcard.EncodeCard(context.Background(), parsed)
	if encodeErr != nil {
		t.Fatal(encodeErr.Code)
	}
	intent := newJobIntent{Version: 1,
		Home:        datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_" + strings.Repeat("a", 32)},
		OperationID: strings.Repeat("e", 32), InitialStatus: "captured", JobID: "synthetic",
		RawPath: "jobs/incoming/synthetic.txt", CardPath: "jobs/normalized/synthetic.yaml",
		RawSize: len(raw), RawSHA256: digestBytes(raw), CardSize: len(card), CardSHA256: digestBytes(card), ContentFingerprint: fingerprint}
	if err := validateFrozenNewJob(context.Background(), intent, raw, card); err != nil {
		t.Fatal(err.Code)
	}
	return intent, raw, card
}

func TestNewJobPublicationAmbiguousNativeCalls(t *testing.T) {
	if home := os.Getenv(newJobPublishHomeEnv); home != "" {
		runNewJobPublicationCase(t, home, os.Getenv(newJobPublishCaseEnv))
		return
	}
	for _, scenario := range []string{"success", "intent_effect", "intent_no_effect", "raw_effect", "raw_no_effect", "card_effect", "card_no_effect", "witness_after_raw", "raw_changed", "receipt_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			for _, directory := range []string{"jobs/incoming", "jobs/normalized"} {
				if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(directory)), 0700); err != nil {
					t.Fatal(err)
				}
			}
			intent, _, _ := frozenNewJobFixture(t)
			if scenario == "receipt_conflict" {
				path, receipt, receiptErr := newJobTerminalReceiptFile(intent)
				if receiptErr != nil {
					t.Fatal(receiptErr.Code)
				}
				if err := os.MkdirAll(filepath.Join(home, "jobs", "intake_receipts"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(path)), receipt, 0600); err != nil {
					t.Fatal(err)
				}
			}
			marker, markerErr := datahome.MarkerBytes(intent.Home.Channel, intent.Home.DataHomeID)
			if markerErr != nil {
				t.Fatal(markerErr.Code)
			}
			if err := os.WriteFile(filepath.Join(home, datahome.MarkerPath), marker, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewJobPublicationAmbiguousNativeCalls$")
			cmd.Env = append(os.Environ(), newJobPublishHomeEnv+"="+home, newJobPublishCaseEnv+"="+scenario)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fictional publication failed: %v: %.1200s", err, output)
			}
		})
	}
}

func runNewJobPublicationCase(t *testing.T, home, scenario string) {
	t.Helper()
	intent, raw, card := frozenNewJobFixture(t)
	root, openErr := runtimepaths.Open(home)
	if openErr != nil {
		t.Fatal(openErr.Code)
	}
	if claimErr := root.ClaimForProcess(); claimErr != nil {
		t.Fatal(claimErr.Code)
	}
	defer root.Close()
	ctx := context.Background()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		t.Fatal(gateErr)
	}
	checks := newJobPublicationChecks{witnesses: func(context.Context) *Error { return nil }}
	if scenario == "raw_changed" {
		checks.checkpoint = func(stage string) *Error {
			if stage == "raw" {
				if replaced, err := root.ReplaceFile(intent.RawPath, raw, []byte("changed fictional JD")); err != nil || !replaced {
					t.Fatal("raw drift fixture unavailable")
				}
			}
			return nil
		}
	} else if scenario == "witness_after_raw" {
		calls := 0
		checks.witnesses = func(context.Context) *Error {
			calls++
			if calls == 3 {
				return failed("witness", "JOB_INTAKE_INPUT_CHANGED")
			}
			return nil
		}
	} else if scenario != "success" && scenario != "receipt_conflict" {
		stage, effect, ok := strings.Cut(scenario, "_")
		if !ok || (stage != "intent" && stage != "raw" && stage != "card") || (effect != "effect" && effect != "no_effect") {
			t.Fatal("invalid fixture scenario")
		}
		target := map[string]string{"intent": datahome.NewJobRecoveryPath, "raw": intent.RawPath, "card": intent.CardPath}[stage]
		checks.create = func(root *runtimepaths.Root, path string, payload []byte) (bool, *runtimepaths.Error) {
			if path != target {
				return root.CreateFile(path, payload)
			}
			if effect == "effect" {
				created, err := root.CreateFile(path, payload)
				if err != nil || !created {
					t.Fatal("effect fixture could not publish")
				}
			}
			return false, &runtimepaths.Error{Code: "SYNTHETIC_CREATE_ERROR"}
		}
	}
	facts, problem := publishFrozenNewJob(ctx, root, intent, raw, card, checks)
	release()
	if scenario == "success" {
		if problem != nil || facts.Unconfirmed || !facts.IntentReported || !facts.RawReported || !facts.CardReported ||
			!facts.IntentObserved || !facts.RawObserved || !facts.CardObserved || !facts.recoveryRequired() {
			t.Fatalf("completed file phase misreported: %#v %#v", facts, problem)
		}
	} else if scenario == "receipt_conflict" {
		if problem == nil || problem.Code != "JOB_INTAKE_PATH_CONFLICT" || facts.IntentAttempted || facts.recoveryRequired() {
			t.Fatal("existing terminal receipt did not block new publication", facts, problem)
		}
	} else if scenario == "raw_changed" {
		if problem == nil || problem.Code != "JOB_INTAKE_SOURCE_CHANGED" || !facts.IntentObserved || !facts.RawObserved || facts.CardAttempted || !facts.Unconfirmed {
			t.Fatalf("changed raw source published a card: %#v %#v", facts, problem)
		}
	} else if scenario == "witness_after_raw" {
		if problem == nil || problem.Code != "JOB_INTAKE_INPUT_CHANGED" || !facts.IntentObserved || !facts.RawObserved || facts.CardAttempted || !facts.recoveryRequired() {
			t.Fatalf("changed read set published a card: %#v %#v", facts, problem)
		}
	} else {
		stage, effect, _ := strings.Cut(scenario, "_")
		if problem == nil || problem.Stage != stage || problem.Code != "JOB_INTAKE_FILE_UNCONFIRMED" || !facts.Unconfirmed || !facts.recoveryRequired() {
			t.Fatalf("ambiguous %s misreported: %#v %#v", scenario, facts, problem)
		}
		call := map[string][3]bool{
			"intent": {facts.IntentAttempted, facts.IntentReported, facts.IntentObserved},
			"raw":    {facts.RawAttempted, facts.RawReported, facts.RawObserved},
			"card":   {facts.CardAttempted, facts.CardReported, facts.CardObserved},
		}[stage]
		if !call[0] || call[1] || call[2] != (effect == "effect") {
			t.Fatalf("native call and observed bytes were conflated: %#v", facts)
		}
	}
	journal, journalPresent, journalErr := root.ReadSingleLinkFile(datahome.NewJobRecoveryPath, newJobIntentLimit)
	if journalErr != nil {
		t.Fatal(journalErr.Code)
	}
	sealed, encodeErr := encodeNewJobIntent(intent)
	if encodeErr != nil {
		t.Fatal(encodeErr.Code)
	}
	if journalPresent != facts.IntentObserved || journalPresent && !bytes.Equal(journal, sealed) {
		t.Fatal("pending journal was removed or changed")
	}
	if journalPresent {
		observation, err := New(home).PinnedToHome(intent.Home).ObserveNewJobRecovery(ctx)
		if err != nil || observation == nil || observation.FileState != expectedNewJobFileState(scenario) {
			t.Fatalf("fresh owned read disagreed with publication: %#v %#v", observation, err)
		}
	}
}

func expectedNewJobFileState(scenario string) string {
	switch scenario {
	case "success", "card_effect":
		return string(newJobFilesComplete)
	case "card_no_effect", "raw_effect", "witness_after_raw":
		return string(newJobFilesRawOnly)
	case "raw_changed":
		return string(newJobFilesConflict)
	default:
		return string(newJobFilesNone)
	}
}

func TestNewJobPublicationPreflightRejectsChangedDraft(t *testing.T) {
	intent, raw, card := frozenNewJobFixture(t)
	changed := bytes.Replace(card, []byte("draft"), []byte("ready"), 1)
	if bytes.Equal(card, changed) {
		t.Fatal("draft fixture did not change")
	}
	intent.CardSize, intent.CardSHA256 = len(changed), digestBytes(changed)
	if err := validateFrozenNewJob(context.Background(), intent, raw, changed); err == nil || err.Code != "JOB_INTAKE_CARD_INVALID" {
		t.Fatal("non-draft card passed prepublication validation")
	}
}
