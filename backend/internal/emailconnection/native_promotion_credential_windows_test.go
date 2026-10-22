//go:build windows

package emailconnection

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/secretstore"
)

// A database row may refer to v1, but this adapter refuses every operation
// against the shared channel-scoped credential, including reads.
type promotionNativeBackend struct {
	secretstore.Backend
	legacyAccesses *int
}

func (backend promotionNativeBackend) Write(target string, secret []byte) error {
	if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
		*backend.legacyAccesses += 1
		return errors.New("test refuses v1 credential write")
	}
	return backend.Backend.Write(target, secret)
}

func (backend promotionNativeBackend) Read(target string) ([]byte, bool, error) {
	if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
		*backend.legacyAccesses += 1
		return nil, false, errors.New("test refuses v1 credential read")
	}
	return backend.Backend.Read(target)
}

func (backend promotionNativeBackend) Delete(target string) (bool, error) {
	if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
		*backend.legacyAccesses += 1
		return false, errors.New("test refuses v1 credential deletion")
	}
	return backend.Backend.Delete(target)
}

// Opt-in: this test writes one new random v3 target, never the fixed v1 name.
// The child releases its process-owned Home handles before parent Temp cleanup.
func TestNativeLegacyPromotionCredential(t *testing.T) {
	if testing.Short() || os.Getenv("CAREERHUB_TEST_NATIVE_EMAIL_CREDENTIALS") != "1" {
		t.Skip("set CAREERHUB_TEST_NATIVE_EMAIL_CREDENTIALS=1 for isolated Windows credential test")
	}
	root, phase := os.Getenv("CAREERHUB_NATIVE_PROMOTION_ROOT"), os.Getenv("CAREERHUB_NATIVE_PROMOTION_PHASE")
	if phase == "seed-other" {
		otherPath := filepath.Join(root, "other")
		if _, startup := datahome.BootstrapForProcess(context.Background(), otherPath, "dev"); startup != nil {
			t.Fatal(startup.Code)
		}
		if _, err := schema.PrepareOwned(context.Background(), otherPath); err != nil {
			t.Fatal(err)
		}
		connectionRow(t, otherPath, emailcredentials.RowWitness{
			Present: true, Revision: 1, Enabled: true, SecretName: emailcredentials.LegacyName,
		})
		return
	}
	if phase == "promote" {
		runNativeLegacyPromotionCredential(t, root)
		return
	}
	root = t.TempDir()
	for _, phase := range []string{"seed-other", "promote"} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeLegacyPromotionCredential$", "-test.v")
		command.Env = append(os.Environ(), "CAREERHUB_NATIVE_PROMOTION_PHASE="+phase, "CAREERHUB_NATIVE_PROMOTION_ROOT="+root)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("isolated native promotion %s child failed: %v %.2000s", phase, err, output)
		}
		t.Logf("isolated native promotion %s child: %.2000s", phase, output)
	}
}

func runNativeLegacyPromotionCredential(t *testing.T, root string) {
	t.Helper()
	ctx := context.Background()
	homePath, otherPath := filepath.Join(root, "home"), filepath.Join(root, "other")
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	connectionRow(t, homePath, emailcredentials.RowWitness{
		Present: true, Revision: 1, Enabled: true, SecretName: emailcredentials.LegacyName,
	})
	native, err := secretstore.NewWindowsBackend()
	if err != nil {
		t.Fatal("Windows Credential Manager unavailable for enabled test")
	}
	legacyAccesses := 0
	backend := promotionNativeBackend{Backend: native, legacyAccesses: &legacyAccesses}
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	password := normalizeConnectionPassword("qrst uvwx yzab cdef")
	var name string
	for attempt := 0; attempt < 8; attempt++ {
		candidate, err := testGenerationName(t, homePath, *home)
		if err != nil {
			t.Fatal("test target generation failed")
		}
		_, found, err := store.Get(candidate)
		if err != nil {
			t.Fatal("test target absence could not be established")
		}
		if !found {
			name = candidate
			break
		}
	}
	if name == "" {
		t.Fatal("no absent test credential target available")
	}
	t.Logf("test-owned v3 credential target: CareerHub/%s/provider/%s", home.Channel, name)
	// Record cleanup authority before the first possible write. Never delete an
	// unexpectedly changed target, even if this leaves a test-only credential.
	t.Cleanup(func() {
		value, found, err := store.Get(name)
		if err != nil {
			t.Errorf("test-owned target read failed during cleanup: %v", err)
			return
		}
		if found {
			if value != password {
				t.Errorf("test-owned target changed unexpectedly; refusing cleanup")
				return
			}
			if _, err := store.Delete(name); err != nil {
				t.Errorf("exact test-owned target cleanup failed: %v", err)
				return
			}
		}
		if _, found, err := store.Get(name); err != nil || found {
			t.Errorf("exact test-owned target cleanup unconfirmed: %v", err)
		}
	})
	result, problem := promoteLegacyWithHooks(ctx, homePath, backend, PromoteLegacyInput{
		ExpectedHome: home, AccountAddress: "alerts@example.test", Password: "qrst uvwx yzab cdef",
		JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1,
	}, rotationHooks{generateName: func(datahome.Info) (string, error) { return name, nil }})
	if problem != nil || !result.Committed || !result.IntentRemoved {
		t.Fatalf("native promotion failed: %+v %v", result, problem)
	}
	if legacyAccesses != 0 {
		t.Fatalf("promotion attempted %d shared v1 backend operations", legacyAccesses)
	}
	assertWitness(t, homePath, emailcredentials.RowWitness{
		Present: true, Revision: 2, Enabled: true, SecretName: name,
	})
	assertWitness(t, otherPath, emailcredentials.RowWitness{
		Present: true, Revision: 1, Enabled: true, SecretName: emailcredentials.LegacyName,
	})
	value, found, err := store.Get(name)
	if err != nil || !found || value != password {
		t.Fatalf("new native credential not confirmed: found=%t err=%v", found, err)
	}
}
