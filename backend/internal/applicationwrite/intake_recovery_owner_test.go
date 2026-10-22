//go:build windows

package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/intakeextract"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

const intakeOwnerTestHome = "CAREERHUB_TEST_INTAKE_OWNER_HOME"
const intakeOwnerTestResources = "CAREERHUB_TEST_INTAKE_OWNER_RESOURCES"
const intakeOwnerTestCase = "CAREERHUB_TEST_INTAKE_OWNER_CASE"
const intakeOwnerTestPhase = "CAREERHUB_TEST_INTAKE_OWNER_PHASE"

func TestIntakeRecoveryOwnedDatabase(t *testing.T) {
	if home := os.Getenv(intakeOwnerTestHome); home != "" {
		runIntakeRecoveryOwnedPhase(t, home, os.Getenv(intakeOwnerTestResources), os.Getenv(intakeOwnerTestCase), os.Getenv(intakeOwnerTestPhase))
		return
	}
	for _, scenario := range []string{"none", "intent", "raw", "report", "pre-drift", "raw-drift", "published-drift", "catalog-drift", "catalog-published-drift", "source-missing-pre", "source-missing-raw", "source-missing-published", "source-hardlink-pre", "source-hardlink-raw", "invalid-intent"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			home, resources := filepath.Join(base, "home"), filepath.Join(base, "resources")
			for _, name := range []string{home, resources} {
				if e := os.Mkdir(name, 0700); e != nil {
					t.Fatal(e)
				}
			}
			marker := []byte(`{"application":"CareerHub","format_version":2,"channel":"stable","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`)
			if e := os.WriteFile(filepath.Join(home, datahome.MarkerPath), marker, 0600); e != nil {
				t.Fatal(e)
			}
			for _, phase := range []string{"stage", "recover"} {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeRecoveryOwnedDatabase$")
				cmd.Env = append(os.Environ(), intakeOwnerTestHome+"="+home, intakeOwnerTestResources+"="+resources,
					intakeOwnerTestCase+"="+scenario, intakeOwnerTestPhase+"="+phase)
				output, e := cmd.CombinedOutput()
				cancel()
				if e != nil {
					t.Fatalf("%s failed: %v: %s", phase, e, output)
				}
			}
		})
	}
}

func runIntakeRecoveryOwnedPhase(t *testing.T, home, resourcesPath, scenario, phase string) {
	t.Helper()
	root, e := runtimepaths.Open(home)
	if e != nil {
		t.Fatal(e.Code)
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e.Code)
	}
	defer func() {
		if e := root.Close(); e != nil {
			t.Fatal(e)
		}
	}()
	if phase == "stage" {
		stageIntakeRecoveryOwner(t, root, home, resourcesPath, scenario)
		return
	}
	var p intakeIntent
	if scenario != "none" && scenario != "invalid-intent" {
		sealed, present, e := root.ReadSingleLinkFile(datahome.IntakeRecoveryPath, intakeIntentLimit)
		if e != nil || !present {
			t.Fatal("fixture intent missing")
		}
		p, _ = decodeIntakeIntent(sealed, fixtureIntakeIntent(t).Home)
		if !validIntakeIntent(p) {
			t.Fatal("fixture intent invalid")
		}
	}
	out, problem := New(home).RecoverIntakeWrites(context.Background(), resourcesPath)
	if out == nil {
		t.Fatal("recovery returned no facts")
	}
	switch scenario {
	case "invalid-intent":
		if problem == nil || problem.Code != "INTAKE_INTENT_INVALID" || !out.RecoveryRequired || !intakePresent(t, root, datahome.IntakeRecoveryPath) {
			t.Fatalf("invalid journal not preserved: %+v, %v", out, problem)
		}
	case "raw-drift", "catalog-drift", "source-missing-raw":
		if problem == nil || problem.Code != "INTAKE_WITNESS_CHANGED" || !out.RecoveryRequired || !intakePresent(t, root, datahome.IntakeRecoveryPath) || intakePresent(t, root, p.ReportPath) {
			t.Fatalf("raw-only drift was published: %+v, %v", out, problem)
		}
	case "pre-drift", "source-missing-pre":
		if problem != nil || out.IntakeState != "aborted_before_raw" || out.RecoveryRequired || out.IntakeReportPublished || intakePresent(t, root, datahome.IntakeRecoveryPath) || intakePresent(t, root, p.RawPath) || intakePresent(t, root, p.ReportPath) {
			t.Fatalf("pre-raw drift did not abort: %+v, %v", out, problem)
		}
	case "published-drift", "catalog-published-drift", "source-missing-published":
		if problem != nil || out.IntakeState != "historical_stale" || out.RecoveryRequired || !out.IntakeReportPublished || !intakeFileEquals(root, p.ReportPath, p.Report) {
			t.Fatalf("published stale report lost: %+v, %v", out, problem)
		}
	case "none":
		if problem != nil || out.RecoveryRequired || out.IntakeReportPublished || out.IntakeState != "" || intakePresent(t, root, datahome.IntakeRecoveryPath) {
			t.Fatalf("no-op recovery changed home: %+v, %v", out, problem)
		}
	case "source-hardlink-pre", "source-hardlink-raw":
		if problem == nil || problem.Code != "INTAKE_SOURCE_UNAVAILABLE" || !out.RecoveryRequired || !intakePresent(t, root, datahome.IntakeRecoveryPath) || intakePresent(t, root, p.ReportPath) {
			t.Fatalf("unsafe hardlinked source was adopted or aborted: %+v, %v", out, problem)
		}
	default:
		if problem != nil || out.IntakeState != "recovered_published" || out.RecoveryRequired || !out.IntakeReportPublished || !intakeFileEquals(root, p.ReportPath, p.Report) || !intakeFileEquals(root, p.DiagnosticPath, p.TerminalDiagnostic) {
			t.Fatalf("owned recovery did not settle: %+v, %v", out, problem)
		}
	}
	if !out.RecoveryRequired {
		if info, e := datahome.Inspect(home, nil); e != nil || info == nil {
			t.Fatal("ordinary admission not restored")
		}
		again, e := New(home).RecoverIntakeWrites(context.Background(), resourcesPath)
		if e != nil || again == nil || again.RecoveryRequired || again.IntakeReportPublished || intakePresent(t, root, datahome.IntakeRecoveryPath) {
			t.Fatalf("settled recovery was not idempotent: %+v, %v", again, e)
		}
	}
	if scenario != "none" && scenario != "invalid-intent" {
		db, e := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, filepath.FromSlash(databasePath)))
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		tx, e := db.BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(e)
		}
		witness, p2 := resumelibrary.MutationWitness(context.Background(), tx, p.Resume.ID)
		if p2 != nil {
			t.Fatal(p2.Code)
		}
		var archived sql.NullString
		if e := tx.QueryRow(`SELECT archived_at FROM resume_library WHERE resume_id=?`, p.Resume.ID).Scan(&archived); e != nil {
			t.Fatal(e)
		}
		if e := tx.Rollback(); e != nil {
			t.Fatal(e)
		}
		rowDrift := scenario == "pre-drift" || scenario == "raw-drift" || scenario == "published-drift"
		if rowDrift && (!archived.Valid || witness == p.Resume.Row) || !rowDrift && (archived.Valid || witness != p.Resume.Row) {
			t.Fatal("recovery changed the canonical Resume Version row")
		}
	}
}

func stageIntakeRecoveryOwner(t *testing.T, root *runtimepaths.Root, home, resourcesPath, scenario string) {
	t.Helper()
	if _, e := schema.PrepareOwned(context.Background(), home); e != nil {
		t.Fatalf("fictional schema unavailable: %v", e)
	}
	resources, e := runtimepaths.Open(resourcesPath)
	if e != nil {
		t.Fatal(e.Code)
	}
	defer resources.Close()
	if _, e := resources.EnsureDirectory("src/data"); e != nil {
		t.Fatal(e.Code)
	}
	core := []byte("[]\n")
	for _, name := range []string{catalogstore.CoreGroupsPath, catalogstore.CoreSkillsPath} {
		if created, e := resources.CreateFile(name, core); e != nil || !created {
			t.Fatal("core fixture unavailable")
		}
	}
	if scenario == "none" {
		return
	}
	if scenario == "invalid-intent" {
		if created, e := root.CreateFile(datahome.IntakeRecoveryPath, []byte("opaque invalid intent")); e != nil || !created {
			t.Fatal("invalid intent fixture unavailable")
		}
		return
	}
	p := fixtureIntakeIntent(t)
	source := []byte("Synthetic resume with no private data.")
	libraryPath := resumelibrary.LibraryPath + "/" + p.Resume.ID + ".txt"
	if _, e := root.EnsureDirectory(resumelibrary.LibraryPath); e != nil {
		t.Fatal(e.Code)
	}
	if created, e := root.CreateFile(libraryPath, source); e != nil || !created {
		t.Fatal("source fixture unavailable")
	}
	db, e2 := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, filepath.FromSlash(databasePath)))
	if e2 != nil {
		t.Fatal(e2)
	}
	defer db.Close()
	if _, e := db.Exec(`INSERT INTO resume_library(resume_id,display_name,relative_path,file_name,sha256,size_bytes,added_at) VALUES(?,?,?,?,?,?,?)`,
		p.Resume.ID, "Fictional", libraryPath, p.Resume.FileName, p.Resume.SHA256, p.Resume.Size, "2026-09-13T00:00:00Z"); e != nil {
		t.Fatal(e)
	}
	tx, e2 := db.BeginTx(context.Background(), nil)
	if e2 != nil {
		t.Fatal(e2)
	}
	witness, problem := resumelibrary.MutationWitness(context.Background(), tx, p.Resume.ID)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	if e := tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	p.Resume.Row = witness
	state, catalogProblem := catalogstore.ReadState(intakeCatalogReader{resources}, intakeCatalogReader{root})
	if catalogProblem != nil {
		t.Fatalf("fictional catalog unavailable: %v", catalogProblem.Code)
	}
	p.Catalog[0] = intakeCatalogWitness{Path: catalogstore.CoreGroupsPath, Present: true, Size: len(core), SHA256: digestBytes(core)}
	p.Catalog[2] = intakeCatalogWitness{Path: catalogstore.CoreSkillsPath, Present: true, Size: len(core), SHA256: digestBytes(core)}
	p.RegistryRevision = state.Snapshot.CatalogRevision()
	report, e2 := intakeextract.Extract(context.Background(), source, intakeextract.Source{
		ID: p.SourceID, Path: p.RawPath, FileType: "txt", SHA256: p.Resume.SHA256,
	}, p.RegistryRevision, nil)
	if e2 != nil {
		t.Fatal(e2)
	}
	p.Report, e2 = json.Marshal(report)
	if e2 != nil {
		t.Fatal(e2)
	}
	p.ReportSHA256 = digestBytes(p.Report)
	if !validIntakeIntent(p) {
		t.Fatal("actual fixture intent not valid")
	}
	if scenario == "source-ready" {
		return
	}
	stage := "intent"
	if scenario == "raw" || scenario == "raw-drift" || scenario == "catalog-drift" || scenario == "source-missing-raw" || scenario == "source-hardlink-raw" {
		stage = "raw"
	} else if scenario == "report" || scenario == "published-drift" || scenario == "catalog-published-drift" || scenario == "source-missing-published" {
		stage = "report"
	}
	_, fault := publishFrozenIntake(context.Background(), root, p, source, intakePublicationChecks{
		witnesses: func(context.Context) *Error { return nil },
		checkpoint: func(current string) *Error {
			if current == stage {
				return failed("fixture", "INTAKE_TEST_FAULT")
			}
			return nil
		},
	})
	if fault == nil || fault.Code != "INTAKE_TEST_FAULT" {
		t.Fatal("fixture publication did not stop at requested boundary")
	}
	if strings.Contains(scenario, "drift") && !strings.HasPrefix(scenario, "catalog-") {
		if _, e := db.Exec(`UPDATE resume_library SET archived_at=? WHERE resume_id=?`, "2026-09-13T01:00:00Z", p.Resume.ID); e != nil {
			t.Fatal(e)
		}
	}
	if scenario == "catalog-drift" || scenario == "catalog-published-drift" {
		if _, e := root.EnsureDirectory("career_vault/taxonomy"); e != nil {
			t.Fatal(e.Code)
		}
		if created, e := root.CreateFile(catalogstore.CustomSkillsPath, []byte("[]\n")); e != nil || !created {
			t.Fatal("catalog drift fixture unavailable")
		}
	}
	if scenario == "source-missing-pre" || scenario == "source-missing-raw" || scenario == "source-missing-published" {
		if removed, e := root.RemoveFile(libraryPath, source); e != nil || !removed {
			t.Fatal("missing source fixture unavailable")
		}
	}
	if scenario == "source-hardlink-pre" || scenario == "source-hardlink-raw" {
		if e := os.Link(filepath.Join(home, filepath.FromSlash(libraryPath)), filepath.Join(home, "synthetic-hardlink.txt")); e != nil {
			t.Fatal(e)
		}
	}
}
