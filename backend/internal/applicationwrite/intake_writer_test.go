//go:build windows

package applicationwrite

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/intakeextract"
	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

const intakeWriterTestHome = "CAREERHUB_TEST_INTAKE_WRITER_HOME"
const intakeWriterTestResources = "CAREERHUB_TEST_INTAKE_WRITER_RESOURCES"
const intakeWriterTestCase = "CAREERHUB_TEST_INTAKE_WRITER_CASE"
const intakeWriterTestPhase = "CAREERHUB_TEST_INTAKE_WRITER_PHASE"
const intakeWriterTestFilter = "CAREERHUB_TEST_INTAKE_WRITER_FILTER"

func TestIntakeWriterOwnedProcess(t *testing.T) {
	if home := os.Getenv(intakeWriterTestHome); home != "" {
		runIntakeWriterPhase(t, home, os.Getenv(intakeWriterTestResources), os.Getenv(intakeWriterTestCase), os.Getenv(intakeWriterTestPhase))
		return
	}
	for _, scenario := range []string{"txt", "repeat", "docx", "uppercase-hash", "db-reservation", "unsupported", "hardlink", "source-race", "source-delete", "catalog-race", "collision", "raw-case-alias", "report-limit", "interrupted-intent", "interrupted-raw", "interrupted-report",
		"cleanup-rollback", "cleanup-db", "cleanup-resources", "cleanup-root",
		"ambiguous-intent-effect", "ambiguous-intent-no-effect", "ambiguous-diagnostic-effect", "ambiguous-diagnostic-no-effect",
		"ambiguous-raw-effect", "ambiguous-raw-no-effect", "ambiguous-report-effect", "ambiguous-report-no-effect",
		"ambiguous-terminal-effect", "ambiguous-terminal-no-effect", "ambiguous-remove-effect", "ambiguous-remove-no-effect",
		"exit-intake_reserved", "exit-preflight", "exit-intent", "exit-directory", "exit-diagnostic", "exit-raw", "exit-report", "exit-terminal_diagnostic", "exit-intent_removed"} {
		if filter := os.Getenv(intakeWriterTestFilter); filter != "" && !strings.HasPrefix(scenario, filter) {
			continue
		}
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
			phases := []string{"write"}
			if strings.HasPrefix(scenario, "interrupted-") || strings.HasPrefix(scenario, "exit-") || strings.HasPrefix(scenario, "cleanup-") || strings.HasPrefix(scenario, "ambiguous-") {
				phases = append(phases, "recover")
			}
			for _, phase := range phases {
				ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeWriterOwnedProcess$")
				cmd.Env = append(os.Environ(), intakeWriterTestHome+"="+home, intakeWriterTestResources+"="+resources,
					intakeWriterTestCase+"="+scenario, intakeWriterTestPhase+"="+phase)
				output, e := cmd.CombinedOutput()
				cancel()
				if phase == "write" && strings.HasPrefix(scenario, "exit-") {
					if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 83 {
						t.Fatalf("writer did not exit at %s: %v: %s", scenario, e, output)
					}
					continue
				}
				if e != nil {
					t.Fatalf("%s failed: %v: %s", phase, e, output)
				}
			}
		})
	}
}

func runIntakeWriterPhase(t *testing.T, home, resourcesPath, scenario, phase string) {
	t.Helper()
	root, e := runtimepaths.Open(home)
	if e != nil {
		t.Fatal(e.Code)
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e.Code)
	}
	defer root.Close()
	if phase == "recover" {
		out, problem := New(home).RecoverIntakeWrites(context.Background(), resourcesPath)
		if strings.HasPrefix(scenario, "ambiguous-") {
			expectResume := scenario != "ambiguous-remove-effect" && scenario != "ambiguous-intent-no-effect"
			expectState := "recovered_published"
			if !expectResume {
				expectState = ""
			}
			if problem != nil || out == nil || out.RecoveryRequired || out.IntakeReportPublished != expectResume || out.IntakeState != expectState || intakePresent(t, root, datahome.IntakeRecoveryPath) {
				t.Fatalf("ambiguous writer did not converge safely: %+v, %v", out, problem)
			}
			if scenario == "ambiguous-remove-effect" {
				checkWriterSettledArtifacts(t, root, home, scenario)
			}
			return
		}
		expectResume := scenario != "exit-intake_reserved" && scenario != "exit-preflight" && scenario != "exit-intent_removed" && !strings.HasPrefix(scenario, "cleanup-")
		expectState := ""
		if expectResume {
			expectState = "recovered_published"
		}
		problemStage, problemCode := "", ""
		if problem != nil {
			problemStage, problemCode = problem.Stage, problem.Code
		}
		if problem != nil || out == nil || out.RecoveryRequired || out.IntakeReportPublished != expectResume || out.IntakeState != expectState || intakePresent(t, root, datahome.IntakeRecoveryPath) {
			t.Fatalf("interrupted real writer did not recover: %+v, %s/%s", out, problemStage, problemCode)
		}
		if !expectResume {
			var result struct {
				Recovered bool   `json:"recovered"`
				State     string `json:"state"`
			}
			if json.Unmarshal(out.Result, &result) != nil || result.Recovered || result.State != "none" {
				t.Fatal("absent intent returned a misleading recovery result")
			}
		}
		if strings.HasPrefix(scenario, "exit-") || strings.HasPrefix(scenario, "cleanup-") {
			checkWriterSettledArtifacts(t, root, home, scenario)
			again, againProblem := New(home).RecoverIntakeWrites(context.Background(), resourcesPath)
			if againProblem != nil || again == nil || again.IntakeState != "" || again.RecoveryRequired || intakePresent(t, root, datahome.IntakeRecoveryPath) {
				stage, code := "", ""
				if againProblem != nil {
					stage, code = againProblem.Stage, againProblem.Code
				}
				t.Fatalf("settled writer replay was not idempotent: %+v, %s/%s", again, stage, code)
			}
			checkWriterSettledArtifacts(t, root, home, scenario)
		}
		return
	}
	stageIntakeRecoveryOwner(t, root, home, resourcesPath, "source-ready")
	resources, e := runtimepaths.Open(resourcesPath)
	if e != nil {
		t.Fatal(e.Code)
	}
	defer resources.Close()
	core := []byte("[]\n")
	withSkill := []byte("- {id: synthetic, name: Synthetic, category: tool}\n")
	if committed, e := resources.ReplaceFile(catalogstore.CoreSkillsPath, core, withSkill); e != nil || !committed {
		t.Fatal("fictional matching term unavailable")
	}
	resumeID := fixtureIntakeIntent(t).Resume.ID
	original := []byte("Synthetic resume with no private data.")
	libraryPath := resumelibrary.LibraryPath + "/" + resumeID + ".txt"
	if scenario == "docx" {
		docx := fictionalWriterDOCX(t)
		replaceWriterSource(t, root, home, resumeID, libraryPath, libraryPath[:len(libraryPath)-4]+".docx", docx, "fictional.docx")
	} else if scenario == "unsupported" {
		replaceWriterSource(t, root, home, resumeID, libraryPath, libraryPath[:len(libraryPath)-4]+".pdf", original, "fictional.pdf")
	} else if scenario == "report-limit" {
		// Valid source under the matcher rune budget; duplicated UTF-8 bullet
		// text makes the extraction report exceed the reader's 2 MiB gate.
		content := []byte(strings.Repeat("- "+strings.Repeat("漢", 150)+"\n", 2500))
		replaceWriterSource(t, root, home, resumeID, libraryPath, libraryPath, content, "fictional.txt")
	} else if scenario == "hardlink" {
		if e := os.Link(filepath.Join(home, filepath.FromSlash(libraryPath)), filepath.Join(home, "synthetic-hardlink.txt")); e != nil {
			t.Fatal(e)
		}
	} else if scenario == "uppercase-hash" {
		db, e := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, filepath.FromSlash(databasePath)))
		if e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(`UPDATE resume_library SET sha256=UPPER(sha256) WHERE resume_id=?`, resumeID); e != nil {
			db.Close()
			t.Fatal(e)
		}
		if e := db.Close(); e != nil {
			t.Fatal(e)
		}
	}
	ids := []string{strings.Repeat("a", 32), strings.Repeat("b", 32)}
	used := 0
	s := New(home)
	s.newIntakeID = func() (string, error) {
		index := used
		used++
		if index >= len(ids) {
			return ids[0], nil
		}
		return ids[index], nil
	}
	if scenario == "collision" {
		if _, e := root.EnsureDirectory("career_vault/raw"); e != nil {
			t.Fatal(e.Code)
		}
		rawName := "career_vault/raw/resume_" + digestBytes([]byte(resumeID))[:24] + "_digest_" + ids[0] + ".txt"
		if created, e := root.CreateFile(rawName, []byte("foreign")); e != nil || !created {
			t.Fatal("collision fixture unavailable")
		}
		// Every attempt reuses one collided ID, exercising the eight-attempt cap.
		s.newIntakeID = func() (string, error) { used++; return ids[0], nil }
	}
	if scenario == "raw-case-alias" {
		if _, e := root.EnsureDirectory("career_vault/raw"); e != nil {
			t.Fatal(e.Code)
		}
		stem := "resume_" + digestBytes([]byte(resumeID))[:24] + "_digest_" + ids[0]
		alias := "career_vault/raw/" + strings.ToUpper(stem) + ".TXT"
		if created, e := root.CreateFile(alias, []byte("foreign alias")); e != nil || !created {
			t.Fatal("portable case alias fixture unavailable")
		}
		defer func() {
			if !intakeFileEquals(root, alias, []byte("foreign alias")) {
				t.Fatal("digest changed the pre-existing case alias")
			}
		}()
	}
	if scenario == "source-race" || scenario == "source-delete" || scenario == "catalog-race" {
		s.checkpoint = func(stage string) *Error {
			if stage != "intake_report_prepared" {
				return nil
			}
			if scenario == "source-race" {
				db, e := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, filepath.FromSlash(databasePath)))
				if e != nil {
					t.Fatal(e)
				}
				defer db.Close()
				if _, e := db.Exec(`UPDATE resume_library SET archived_at=? WHERE resume_id=?`, "2026-09-13T01:00:00Z", resumeID); e != nil {
					t.Fatal(e)
				}
			} else if scenario == "source-delete" {
				if removed, e := root.RemoveFile(libraryPath, original); e != nil || !removed {
					t.Fatal("fictional source deletion failed")
				}
			} else {
				if _, e := root.EnsureDirectory("career_vault/taxonomy"); e != nil {
					t.Fatal(e.Code)
				}
				if created, e := root.CreateFile(catalogstore.CustomSkillsPath, []byte("[]\n")); e != nil || !created {
					t.Fatal("catalog drift unavailable")
				}
			}
			return nil
		}
	}
	if scenario == "db-reservation" {
		s.checkpoint = func(stage string) *Error {
			if stage != "intake_reserved" {
				return nil
			}
			independent, e := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, filepath.FromSlash(databasePath)))
			if e != nil {
				t.Fatal("independent SQLite connection could not open")
			}
			defer independent.Close()
			probeCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			if _, e := independent.ExecContext(probeCtx, `UPDATE resume_library SET display_name='Unexpected' WHERE resume_id=?`, resumeID); e == nil {
				t.Fatal("independent SQLite write bypassed the digest reservation")
			}
			return nil
		}
	}
	if strings.HasPrefix(scenario, "interrupted-") {
		fault := strings.TrimPrefix(scenario, "interrupted-")
		s.checkpoint = func(stage string) *Error {
			if stage == fault {
				return failed("fixture", "INTAKE_TEST_FAULT")
			}
			return nil
		}
	}
	if strings.HasPrefix(scenario, "ambiguous-") {
		parts := strings.Split(strings.TrimPrefix(scenario, "ambiguous-"), "-")
		target, effect := parts[0], parts[1] == "effect"
		selectedCreate := func(name string) bool {
			switch target {
			case "intent":
				return name == datahome.IntakeRecoveryPath
			case "diagnostic":
				return strings.Contains(name, "/logs/native-intake/")
			case "raw":
				return strings.Contains(name, "/raw/")
			case "report":
				return strings.Contains(name, "/intake_runs/")
			}
			return false
		}
		s.intakeMutations = intakeFileMutations{
			create: func(r *runtimepaths.Root, name string, content []byte) (bool, *runtimepaths.Error) {
				if !selectedCreate(name) {
					return r.CreateFile(name, content)
				}
				if effect {
					if created, problem := r.CreateFile(name, content); problem != nil || !created {
						return created, problem
					}
				}
				return false, &runtimepaths.Error{Code: "FILE_AMBIGUOUS_TEST"}
			},
			replace: func(r *runtimepaths.Root, name string, expected, replacement []byte) (bool, *runtimepaths.Error) {
				if target != "terminal" {
					return r.ReplaceFile(name, expected, replacement)
				}
				if effect {
					if committed, problem := r.ReplaceFile(name, expected, replacement); problem != nil || !committed {
						return committed, problem
					}
				}
				return false, &runtimepaths.Error{Code: "FILE_AMBIGUOUS_TEST"}
			},
			remove: func(r *runtimepaths.Root, name string, expected []byte) (bool, *runtimepaths.Error) {
				if target != "remove" {
					return r.RemoveFile(name, expected)
				}
				if effect {
					if removed, problem := r.RemoveFile(name, expected); problem != nil || !removed {
						return removed, problem
					}
				}
				return false, &runtimepaths.Error{Code: "FILE_AMBIGUOUS_TEST"}
			},
		}
	}
	if strings.HasPrefix(scenario, "exit-") {
		exitStage := strings.TrimPrefix(scenario, "exit-")
		s.checkpoint = func(stage string) *Error {
			if stage == exitStage {
				os.Exit(83) // fictional child only: bypass all cleanup as an abrupt process loss
			}
			return nil
		}
	}
	if strings.HasPrefix(scenario, "cleanup-") {
		published := false
		s.checkpoint = func(stage string) *Error {
			if stage == "intent_removed" {
				published = true
			}
			return nil
		}
		actualRollback := s.rollback
		s.rollback = func(tx *sql.Tx) error {
			if e := actualRollback(tx); e != nil {
				return e
			}
			if published && scenario == "cleanup-rollback" {
				return errors.New("fictional post-rollback failure")
			}
			return nil
		}
		actualCloseDB := s.closeDB
		s.closeDB = func(db *sql.DB) error {
			if e := actualCloseDB(db); e != nil {
				return e
			}
			if published && scenario == "cleanup-db" {
				return errors.New("fictional post-database-close failure")
			}
			return nil
		}
		actualCloseRoot := s.closeRoot
		rootCloses := 0
		s.closeRoot = func(r *runtimepaths.Root) error {
			rootCloses++
			if e := actualCloseRoot(r); e != nil {
				return e
			}
			if published && (scenario == "cleanup-resources" && rootCloses == 1 || scenario == "cleanup-root" && rootCloses == 2) {
				return errors.New("fictional post-root-close failure")
			}
			return nil
		}
	}
	iterations := 1
	if scenario == "repeat" {
		iterations = 2
	}
	var before intakeSourceSnapshot
	if scenario == "txt" || scenario == "repeat" || scenario == "docx" || scenario == "uppercase-hash" {
		var problem *Error
		before, problem = s.readIntakeSource(context.Background(), root, resumeID)
		if problem != nil || before.identity.ID != resumeID {
			t.Fatal("initial Resume Version witness unavailable")
		}
	}
	var priorSource string
	for i := 0; i < iterations; i++ {
		out, problem := s.DigestResumeVersion(context.Background(), resourcesPath, resumeID)
		switch scenario {
		case "unsupported":
			expectWriterRefusal(t, root, out, problem, "INTAKE_FORMAT_UNSUPPORTED")
		case "hardlink":
			expectWriterRefusal(t, root, out, problem, "INTAKE_SOURCE_UNAVAILABLE")
		case "source-race", "source-delete", "catalog-race":
			expectWriterRefusal(t, root, out, problem, "INTAKE_WITNESS_CHANGED")
			for _, name := range []string{"career_vault/intake_runs", "career_vault/logs/native-intake"} {
				if present, e := root.Exists(name); e != nil || present {
					t.Fatal("changed live input left a new intake artifact")
				}
			}
			if scenario == "source-delete" {
				if present, e := root.Exists("career_vault/raw"); e != nil || present {
					t.Fatal("deleted source left a raw intake copy")
				}
			}
		case "collision":
			if used != intakeCandidateLimit {
				t.Fatal("candidate attempts were not bounded")
			}
			expectWriterRefusal(t, root, out, problem, "INTAKE_IDENTITY_COLLISION")
		case "raw-case-alias":
			expectWriterRefusal(t, root, out, problem, "PATH_CASE_COLLISION")
			if entries, e := os.ReadDir(filepath.Join(home, "career_vault", "raw")); e != nil || len(entries) != 1 {
				t.Fatal("portable case alias created another raw file")
			}
			for _, name := range []string{"career_vault/intake_runs", "career_vault/logs/native-intake"} {
				if present, e := root.Exists(name); e != nil || present {
					t.Fatal("portable case alias left a new intake artifact")
				}
			}
		case "report-limit":
			expectWriterRefusal(t, root, out, problem, "INTAKE_REPORT_LIMIT")
			stem := "resume_" + digestBytes([]byte(resumeID))[:24] + "_digest_" + ids[0]
			sourceID := "src_" + intakeSlug(stem) + "_" + digestBytes(writerSourceBytes(t, root, libraryPath, scenario))[:10]
			for _, name := range []string{
				"career_vault/raw/" + stem + ".txt",
				"career_vault/intake_runs/" + sourceID,
				"career_vault/logs/native-intake/" + ids[0] + ".json",
			} {
				if present, e := root.Exists(name); e != nil || present {
					t.Fatal("over-limit report left an intake artifact")
				}
			}
		case "interrupted-intent", "interrupted-raw", "interrupted-report":
			if problem == nil || problem.Code != "INTAKE_TEST_FAULT" || out == nil || !out.RecoveryRequired || !intakePresent(t, root, datahome.IntakeRecoveryPath) {
				t.Fatalf("interrupted writer hid recovery: %+v, %v", out, problem)
			}
			if scenario == "interrupted-report" && !out.IntakeReportPublished {
				t.Fatal("published report lost its observed fact")
			}
		case "ambiguous-intent-effect", "ambiguous-intent-no-effect", "ambiguous-diagnostic-effect", "ambiguous-diagnostic-no-effect",
			"ambiguous-raw-effect", "ambiguous-raw-no-effect", "ambiguous-report-effect", "ambiguous-report-no-effect",
			"ambiguous-terminal-effect", "ambiguous-terminal-no-effect", "ambiguous-remove-effect", "ambiguous-remove-no-effect":
			wantRecovery := scenario != "ambiguous-intent-no-effect" && scenario != "ambiguous-remove-effect"
			if problem == nil || problem.Code != "FILE_AMBIGUOUS_TEST" || out == nil || out.RecoveryRequired != wantRecovery {
				t.Fatalf("ambiguous native result was presented as safe: %+v, %v", out, problem)
			}
			// ReportPublished is intentionally an attribution fact for the
			// current call. Exact bytes left by a false+error report create are
			// adopted only by the fresh-process recovery owner.
			wantReport := strings.HasPrefix(scenario, "ambiguous-terminal-") || strings.HasPrefix(scenario, "ambiguous-remove-")
			if out.IntakeReportPublished != wantReport {
				t.Fatalf("ambiguous report observation lost: got %v want %v", out.IntakeReportPublished, wantReport)
			}
			if scenario == "ambiguous-remove-effect" && out.IntakeState != "published_unconfirmed" {
				t.Fatalf("completed but unattributed removal lost its distinct state: %+v", out)
			}
			wantIntent := scenario != "ambiguous-intent-no-effect" && scenario != "ambiguous-remove-effect"
			if intakePresent(t, root, datahome.IntakeRecoveryPath) != wantIntent {
				t.Fatal("ambiguous writer left an unexpected recovery sentinel state")
			}
		case "cleanup-rollback", "cleanup-db", "cleanup-resources", "cleanup-root":
			wantCode := map[string]string{
				"cleanup-rollback":  "INTAKE_ROLLBACK_FAILED",
				"cleanup-db":        "INTAKE_DATABASE_CLOSE_FAILED",
				"cleanup-resources": "INTAKE_RESOURCES_CLOSE_FAILED",
				"cleanup-root":      "INTAKE_ROOT_CLOSE_FAILED",
			}[scenario]
			if problem == nil || problem.Code != wantCode || out == nil || !out.CleanupFailed || out.CleanupCode != wantCode ||
				!out.IntakeReportPublished || out.IntakeState != "published" || out.RecoveryRequired || out.IntakeSourceID == "" || out.Result != nil {
				t.Fatalf("post-action cleanup lost published facts: %+v, %s", out, writerProblemCode(problem))
			}
			checkWriterSettledArtifacts(t, root, home, scenario)
		default:
			if problem != nil || out == nil || out.CleanupFailed || out.RecoveryRequired || !out.IntakeReportPublished || out.IntakeState != "published" || intakePresent(t, root, datahome.IntakeRecoveryPath) {
				t.Fatalf("writer did not publish cleanly: %+v, %v", out, problem)
			}
			var result struct {
				ResumeID        string `json:"resume_id"`
				SourceID        string `json:"source_id"`
				TechnologyCount int    `json:"technology_count"`
				BulletCount     int    `json:"bullet_count"`
			}
			if json.Unmarshal(out.Result, &result) != nil || result.ResumeID != resumeID || result.SourceID != out.IntakeSourceID || result.SourceID == priorSource || result.TechnologyCount != 1 {
				t.Fatalf("source-shaped result invalid: %+v", result)
			}
			priorSource = result.SourceID
			raw, present, e := root.ReadSingleLinkFile("career_vault/intake_runs/"+result.SourceID+"/extracted.yaml", intakeread.MaxExtractionBytes)
			if e != nil || !present {
				t.Fatal("published report missing")
			}
			parsed, e2 := intakeread.ParseExtraction(raw)
			if e2 != nil || parsed.Source.ID != result.SourceID || parsed.ExtractorVersion != intakeextract.ExtractorVersion {
				t.Fatal("published report not a valid native extraction")
			}
			var full intakeextract.Report
			if json.Unmarshal(raw, &full) != nil || len(full.TechnologyIDs) != result.TechnologyCount || len(full.BulletCandidates) != result.BulletCount || !intakeFileEquals(root, full.Source.Path, writerSourceBytes(t, root, libraryPath, scenario)) {
				t.Fatal("published extraction/source mismatch")
			}
		}
	}
	if before.identity.ID != "" {
		after, problem := s.readIntakeSource(context.Background(), root, resumeID)
		if problem != nil || after.identity != before.identity || !bytes.Equal(after.content, before.content) {
			t.Fatal("file-only digest mutated the Resume Version")
		}
	}
	if scenario == "db-reservation" {
		independent, e := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, filepath.FromSlash(databasePath)))
		if e != nil {
			t.Fatal("post-digest SQLite connection unavailable")
		}
		if _, e := independent.Exec(`UPDATE resume_library SET display_name='Independent Writer' WHERE resume_id=?`, resumeID); e != nil {
			independent.Close()
			t.Fatal("digest reservation remained after cleanup")
		}
		var display string
		if e := independent.QueryRow(`SELECT display_name FROM resume_library WHERE resume_id=?`, resumeID).Scan(&display); e != nil || display != "Independent Writer" {
			independent.Close()
			t.Fatal("independent SQLite update did not persist after digest")
		}
		if e := independent.Close(); e != nil {
			t.Fatal("independent SQLite connection did not close")
		}
	}
	if strings.HasPrefix(scenario, "ambiguous-") {
		_, admission := datahome.Inspect(home, nil)
		pending := scenario != "ambiguous-intent-no-effect" && scenario != "ambiguous-remove-effect"
		if pending && admission == nil || !pending && admission != nil {
			t.Fatal("ambiguous writer admission did not match its exact sentinel state")
		}
	} else if info, e := datahome.Inspect(home, nil); scenario != "interrupted-intent" && scenario != "interrupted-raw" && scenario != "interrupted-report" && (e != nil || info == nil) {
		t.Fatal("writer did not restore ordinary admission")
	}
}

func expectWriterRefusal(t *testing.T, root *runtimepaths.Root, out *Outcome, problem *Error, code string) {
	t.Helper()
	if problem == nil || problem.Code != code || out == nil || out.IntakeReportPublished || out.RecoveryRequired || intakePresent(t, root, datahome.IntakeRecoveryPath) {
		got := "<nil>"
		if problem != nil {
			got = problem.Code
		}
		t.Fatalf("prepublication refusal: got %s want %s, outcome %+v", got, code, out)
	}
}

func checkWriterSettledArtifacts(t *testing.T, root *runtimepaths.Root, home, scenario string) {
	t.Helper()
	resume := fixtureIntakeIntent(t).Resume
	resumeID := resume.ID
	source := []byte("Synthetic resume with no private data.")
	operationID := strings.Repeat("a", 32)
	stem := "resume_" + digestBytes([]byte(resumeID))[:24] + "_digest_" + operationID
	rawPath := "career_vault/raw/" + stem + ".txt"
	sourceID := "src_" + intakeSlug(stem) + "_" + digestBytes(source)[:10]
	runPath := "career_vault/intake_runs/" + sourceID
	reportPath := runPath + "/extracted.yaml"
	diagnosticPath := "career_vault/logs/native-intake/" + operationID + ".json"
	published := scenario != "exit-intake_reserved" && scenario != "exit-preflight"
	if intakePresent(t, root, datahome.IntakeRecoveryPath) {
		t.Fatal("settled process exit left an intake intent")
	}
	if !published {
		for _, name := range []string{rawPath, runPath, reportPath, diagnosticPath} {
			if present, e := root.Exists(name); e != nil || present {
				t.Fatal("prepublication process exit left an intake artifact")
			}
		}
	} else {
		if !intakeFileEquals(root, rawPath, source) {
			t.Fatal("recovery did not preserve the exact raw Resume Version")
		}
		raw, present, e := root.ReadSingleLinkFile(reportPath, intakeread.MaxExtractionBytes)
		if e != nil || !present {
			t.Fatal("recovery did not publish one extraction report")
		}
		report, e2 := intakeread.ParseExtraction(raw)
		if e2 != nil || report.Source.ID != sourceID || report.Source.Path != rawPath || report.Source.SHA256 != digestBytes(source) {
			t.Fatal("recovered report lost exact source identity")
		}
		diagnostic, present, e := root.ReadSingleLinkFile(diagnosticPath, intakeDiagnosticLimit)
		var status intakeDiagnostic
		if e != nil || !present || json.Unmarshal(diagnostic, &status) != nil || status.State != "success" || status.SourceID != sourceID || status.OperationID != operationID {
			t.Fatal("recovered diagnostic was not terminal")
		}
		for _, directory := range []string{"career_vault/raw", "career_vault/intake_runs", "career_vault/logs/native-intake"} {
			entries, e := os.ReadDir(filepath.Join(home, filepath.FromSlash(directory)))
			if e != nil || len(entries) != 1 {
				t.Fatal("recovery duplicated or omitted an owned artifact")
			}
		}
	}
	if info, e := datahome.Inspect(home, nil); e != nil || info == nil {
		t.Fatal("settled process exit did not restore ordinary admission")
	}
	if !intakeFileEquals(root, resumelibrary.LibraryPath+"/"+resumeID+".txt", source) {
		t.Fatal("process exit changed the stored Resume Version file")
	}
	db, e := sqlitestore.OpenReadOnly(context.Background(), filepath.Join(home, filepath.FromSlash(databasePath)))
	if e != nil {
		t.Fatal("stored Resume Version database unavailable")
	}
	defer db.Close()
	var id, display, relativePath, fileName, sha string
	var size, count int
	var archived sql.NullString
	if e := db.QueryRow(`SELECT resume_id,display_name,relative_path,file_name,sha256,size_bytes,archived_at FROM resume_library WHERE resume_id=?`, resumeID).
		Scan(&id, &display, &relativePath, &fileName, &sha, &size, &archived); e != nil ||
		id != resumeID || display != "Fictional" || relativePath != resumelibrary.LibraryPath+"/"+resumeID+".txt" ||
		fileName != resume.FileName || sha != digestBytes(source) || size != len(source) || archived.Valid {
		t.Fatal("process exit changed the stored Resume Version row")
	}
	if e := db.QueryRow(`SELECT COUNT(*) FROM resume_library`).Scan(&count); e != nil || count != 1 {
		t.Fatal("process exit added a Resume Version row")
	}
}

func writerProblemCode(problem *Error) string {
	if problem == nil {
		return ""
	}
	return problem.Code
}

func replaceWriterSource(t *testing.T, root *runtimepaths.Root, home, id, beforePath, afterPath string, content []byte, fileName string) {
	t.Helper()
	if beforePath == afterPath {
		if committed, e := root.ReplaceFile(beforePath, []byte("Synthetic resume with no private data."), content); e != nil || !committed {
			t.Fatal("fictional source replacement failed")
		}
	} else if created, e := root.CreateFile(afterPath, content); e != nil || !created {
		t.Fatal("fictional source variant unavailable")
	}
	db, e := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, filepath.FromSlash(databasePath)))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e := db.Exec(`UPDATE resume_library SET relative_path=?,file_name=?,sha256=?,size_bytes=? WHERE resume_id=?`,
		afterPath, fileName, digestBytes(content), len(content), id); e != nil {
		t.Fatal(e)
	}
}

func writerSourceBytes(t *testing.T, root *runtimepaths.Root, original string, scenario string) []byte {
	t.Helper()
	name := original
	if scenario == "docx" {
		name = original[:len(original)-4] + ".docx"
	}
	raw, present, e := root.ReadSingleLinkFile(name, 10<<20)
	if e != nil || !present {
		t.Fatal("fictional source unavailable")
	}
	return raw
}

func fictionalWriterDOCX(t *testing.T) []byte {
	t.Helper()
	const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	const typesNS = "http://schemas.openxmlformats.org/package/2006/content-types"
	const relNS = "http://schemas.openxmlformats.org/package/2006/relationships"
	const officeNS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	parts := [][2]string{
		{"[Content_Types].xml", `<Types xmlns="` + typesNS + `"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="` + relNS + `"><Relationship Id="rId1" Type="` + officeNS + `officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/_rels/document.xml.rels", `<Relationships xmlns="` + relNS + `"><Relationship Id="rId1" Type="` + officeNS + `styles" Target="styles.xml"/></Relationships>`},
		{"word/styles.xml", `<w:styles xmlns:w="` + wordNS + `"><w:style w:type="paragraph" w:styleId="Normal" w:default="1"><w:name w:val="Normal"/></w:style></w:styles>`},
		{"word/document.xml", `<w:document xmlns:w="` + wordNS + `"><w:body><w:p><w:r><w:t>Synthetic</w:t></w:r></w:p></w:body></w:document>`},
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, part := range parts {
		f, e := writer.Create(part[0])
		if e != nil {
			t.Fatal(e)
		}
		if _, e := f.Write([]byte(part[1])); e != nil {
			t.Fatal(e)
		}
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	return buffer.Bytes()
}
