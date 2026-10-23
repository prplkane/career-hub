//go:build windows

package emailconnection

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/storage/datahome"
)

func TestCredentialMutationsPinStartupHome(t *testing.T) {
	if os.Getenv("CAREERHUB_EMAIL_HOME_PIN_CHILD") == "1" {
		runCredentialHomePin(t, os.Getenv("CAREERHUB_EMAIL_HOME_PIN_ROOT"))
		return
	}
	root := filepath.Join(t.TempDir(), "home")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCredentialMutationsPinStartupHome$", "-test.v")
	child.Env = append(os.Environ(), "CAREERHUB_EMAIL_HOME_PIN_CHILD=1", "CAREERHUB_EMAIL_HOME_PIN_ROOT="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("synthetic Home-pin child failed: %v %.2000s", err, output)
	}
}

func runCredentialHomePin(t *testing.T, root string) {
	t.Helper()
	ctx := context.Background()
	home, startup := datahome.BootstrapForProcess(ctx, root, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, root); err != nil {
		t.Fatal(err)
	}
	wrong := *home
	wrong.DataHomeID = "dh_00000000000000000000000000000000"
	if wrong.DataHomeID == home.DataHomeID {
		wrong.DataHomeID = "dh_11111111111111111111111111111111"
	}
	backend := &fakeSecrets{}
	initial := validNewInput()
	initial.ExpectedHome = &wrong
	if result, problem := ConfigureNew(ctx, root, backend, initial); problem == nil || problem.Code != "DATA_HOME_CHANGED" || result != (ConfigureResult{}) {
		t.Fatalf("wrong startup Home reached initial publication: %+v %v", result, problem)
	}
	assertWitness(t, root, emailcredentials.RowWitness{})
	if len(backend.values) != 0 {
		t.Fatal("wrong startup Home touched credential backend")
	}
	if _, err := os.Stat(filepath.Join(root, datahome.EmailCredentialRecoveryPath)); !os.IsNotExist(err) {
		t.Fatalf("wrong startup Home published recovery intent: %v", err)
	}
	initial.ExpectedHome = home
	configured, problem := ConfigureNew(ctx, root, backend, initial)
	if problem != nil || !configured.Committed || configured.Revision != 1 {
		t.Fatalf("matching Home initial configure failed: %+v %v", configured, problem)
	}
	witness, _, _, _, _ := readConfigureRow(t, root)
	if !witness.Present || len(backend.values) != 1 {
		t.Fatal("matching Home did not publish one connection/credential")
	}
	settings := EditSettingsInput{
		ExpectedHome: &wrong, AccountAddress: "alerts@example.test",
		JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1,
	}
	if result, problem := EditSettings(ctx, root, backend, settings); problem == nil || problem.Code != "DATA_HOME_CHANGED" || result != (EditSettingsResult{}) {
		t.Fatalf("wrong startup Home reached settings transaction: %+v %v", result, problem)
	}
	assertWitness(t, root, witness)
	settings.ExpectedHome = home
	changed, problem := EditSettings(ctx, root, backend, settings)
	if problem != nil || !changed.Committed || changed.Revision != 2 {
		t.Fatalf("matching Home settings edit failed: %+v %v", changed, problem)
	}
	witness.Revision = 2
	rotation := RotateInput{
		ExpectedHome: &wrong, AccountAddress: "alerts@example.test", Password: "qrst uvwx yzab cdef",
		JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 2,
	}
	if result, problem := Rotate(ctx, root, backend, rotation); problem == nil || problem.Code != "DATA_HOME_CHANGED" || result != (RotateResult{}) {
		t.Fatalf("wrong startup Home reached rotation publication: %+v %v", result, problem)
	}
	assertWitness(t, root, witness)
	if len(backend.values) != 1 {
		t.Fatal("wrong startup Home changed credential generations")
	}
	rotation.ExpectedHome = home
	rotated, problem := Rotate(ctx, root, backend, rotation)
	if problem != nil || !rotated.Committed || rotated.Revision != 3 || len(backend.values) != 1 {
		t.Fatalf("matching Home rotation failed: %+v %v", rotated, problem)
	}
	witness, _, _, _, _ = readConfigureRow(t, root)
	if result, problem := DisconnectForHome(ctx, root, backend, wrong, 3); problem == nil || problem.Code != "DATA_HOME_CHANGED" || result != (RetireResult{}) {
		t.Fatalf("wrong startup Home reached disconnect publication: %+v %v", result, problem)
	}
	assertWitness(t, root, witness)
	if len(backend.values) != 1 {
		t.Fatal("wrong startup Home deleted active credential")
	}
	disconnected, problem := DisconnectForHome(ctx, root, backend, *home, 3)
	if problem != nil || !disconnected.Committed || disconnected.Revision != 4 || len(backend.values) != 0 {
		t.Fatalf("matching Home disconnect failed: %+v %v", disconnected, problem)
	}
	witness.Revision, witness.Enabled = 4, false
	if result, problem := ForgetForHome(ctx, root, backend, wrong, 4); problem == nil || problem.Code != "DATA_HOME_CHANGED" || result != (RetireResult{}) {
		t.Fatalf("wrong startup Home reached forget publication: %+v %v", result, problem)
	}
	assertWitness(t, root, witness)
	forgotten, problem := ForgetForHome(ctx, root, backend, *home, 4)
	if problem != nil || !forgotten.Committed || len(backend.values) != 0 {
		t.Fatalf("matching Home forget failed: %+v %v", forgotten, problem)
	}
	assertWitness(t, root, emailcredentials.RowWitness{})
}
