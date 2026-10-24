//go:build windows

package emailcredentials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

type nativeScopedFixture struct {
	Targets []string `json:"targets"`
	Intents [][]byte `json:"intents"`
}

// Real process-owned scopes, synthetic Home metadata and nonsecret journals.
// This helper has no OS-secret backend and cannot access personal credentials.
func TestScopedCodecNativeHelper(t *testing.T) {
	path := os.Getenv("CAREERHUB_SCOPED_CODEC_TEST_ROOT")
	if path == "" {
		return
	}
	root, e := runtimepaths.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	scope, e := root.CredentialScope()
	if e != nil {
		t.Fatal(e)
	}
	if input := os.Getenv("CAREERHUB_SCOPED_CODEC_TEST_INPUT"); input != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input)
		if err != nil {
			t.Fatal(err)
		}
		var fixture nativeScopedFixture
		if err := json.Unmarshal(raw, &fixture); err != nil {
			t.Fatal(err)
		}
		matches := os.Getenv("CAREERHUB_SCOPED_CODEC_TEST_MATCH") == "yes"
		if len(fixture.Targets) != 2 || len(fixture.Intents) != 7 {
			t.Fatal("missing fixture")
		}
		for _, target := range fixture.Targets {
			err := ValidateScopedTarget(testHome, scope, target)
			if matches && err != nil || !matches && !errors.Is(err, ErrInvalidTarget) {
				t.Fatal("native target admission incorrect")
			}
			reconnect, err := RequiresReauthorization(testHome, scope, target)
			if err != nil || reconnect == matches {
				t.Fatal("native reconnect classification incorrect")
			}
		}
		for _, raw := range fixture.Intents {
			intent, err := DecodeScopedIntent(raw, testHome, scope)
			if matches && err != nil || !matches && (!errors.Is(err, ErrIntentScopeChanged) || intent != (ScopedIntent{})) {
				t.Fatal("native journal admission incorrect")
			}
		}
	}
	// Every root can create and validate its own independent names/journals.
	var fixture nativeScopedFixture
	for i := 0; i < 2; i++ {
		name, err := NewScopedGenerationName(testHome, scope)
		if err != nil || ValidateScopedTarget(testHome, scope, name) != nil {
			t.Fatal("native v3 generation failed")
		}
		fixture.Targets = append(fixture.Targets, name)
	}
	if fixture.Targets[0] == fixture.Targets[1] {
		t.Fatal("random generation reused target")
	}
	oldV2, err := NewGenerationName(testHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{LegacyName, oldV2} {
		if reconnect, err := RequiresReauthorization(testHome, scope, old); !reconnect || err != nil {
			t.Fatal("legacy metadata classification incorrect")
		}
	}
	if reconnect, err := RequiresReauthorization(testHome, scope, "arbitrary-target"); reconnect || !errors.Is(err, ErrInvalidTarget) {
		t.Fatal("arbitrary pointer eligible for reconnect")
	}
	for _, operation := range []string{InitialConfigure, Rotate, Disconnect, Forget, AdoptUnbound, AdoptUnbound, RebindCopied} {
		intent := sampleScopedIntent(operation)
		intent.RootFingerprint = scope.Fingerprint()
		if intent.Before.Present && operation != AdoptUnbound && operation != RebindCopied {
			intent.Before.SecretName = fixture.Targets[0]
		}
		if intent.After.Present {
			intent.After.SecretName = fixture.Targets[1]
		}
		if operation == Disconnect {
			intent.After.SecretName = intent.Before.SecretName
		}
		if operation == AdoptUnbound && len(fixture.Intents) == 5 {
			intent.Before.SecretName = LegacyName
		}
		raw, err := EncodeScopedIntent(intent, scope)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeScopedIntent(raw, testHome, scope)
		if err != nil || decoded != intent {
			t.Fatal("native intent roundtrip failed")
		}
		presence := CredentialObservation{}
		if operation == InitialConfigure || operation == Rotate || operation == AdoptUnbound || operation == RebindCopied {
			presence.NewFound = observed(true)
		}
		if operation == Rotate || operation == Disconnect || operation == Forget {
			presence.OldFound = observed(true)
		}
		plan, err := PlanScopedRecovery(intent, scope, intent.After, presence)
		if err != nil || plan.DatabaseSide != AfterCommit || plan.LegacyPreflightRequired ||
			((operation == AdoptUnbound || operation == RebindCopied) && plan.Cleanup != CleanupNone) {
			t.Fatal("native scoped recovery planning failed")
		}
		fixture.Intents = append(fixture.Intents, raw)
	}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("fixture=" + base64.RawURLEncoding.EncodeToString(raw))
}

func TestScopedCodecNativeRestartCopyMoveAndReplacement(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b, moved := filepath.Join(base, "A"), filepath.Join(base, "B"), filepath.Join(base, "Moved")
	for _, path := range []string{a, b} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(path, fixture string, matches bool) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScopedCodecNativeHelper$")
		match := "no"
		if matches {
			match = "yes"
		}
		cmd.Env = append(os.Environ(), "CAREERHUB_SCOPED_CODEC_TEST_ROOT="+path,
			"CAREERHUB_SCOPED_CODEC_TEST_INPUT="+fixture, "CAREERHUB_SCOPED_CODEC_TEST_MATCH="+match)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("scoped codec helper failed: %s", out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "fixture=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "fixture="))
			}
		}
		t.Fatal("helper returned no fixture")
		return ""
	}
	original := run(a, "", true)
	run(a, original, true)
	run(strings.ToUpper(a), original, true)
	copyFixture := run(b, original, false)
	run(b, copyFixture, true)
	run(a, copyFixture, false)
	run(a, original, true) // Creating B's targets did not affect A's admission.
	if err := os.Rename(a, moved); err != nil {
		t.Fatal(err)
	}
	run(moved, original, false)
	if err := os.Mkdir(a, 0700); err != nil {
		t.Fatal(err)
	}
	run(a, original, false)
}
