package readonly

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intake/intakeextract"
	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const resumeRunsPath = "career_vault/intake_runs"
const maxResumeReportBytes = 32 << 20

// ManagedResumeEntry is the browser-shaped projection; private digest identities
// remain only in the owned snapshot and are never serialized.
type ManagedResumeEntry struct {
	applications.ResumeEntry
	DigestStatus      string  `json:"digest_status"`
	DigestSupported   bool    `json:"digest_supported"`
	DigestSourceID    *string `json:"digest_source_id"`
	DigestProcessedAt *string `json:"digest_processed_at"`
}

// ResumeManagement owns one admitted, read-only request. It is not yet routed.
func (w *Workspace) ResumeManagement(ctx context.Context, filters applications.ResumeFilters) ([]ManagedResumeEntry, error) {
	if err := applications.ValidateResumeFilters(filters); err != nil {
		return nil, applicationReadError(err)
	}
	if !filters.IncludeArchived {
		return nil, fail("APPLICATION_INPUT_UNSUPPORTED")
	}
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) ([]ManagedResumeEntry, error) {
		return readResumeManagement(ctx, w.start.DataRoot, data, resources, filters)
	})
}

func managementSnapshot(ctx context.Context, data *runtimepaths.Root, dataRoot, search string) (result *applications.ResumeManagementSnapshot, failure error) {
	guard, e := data.GuardSQLiteFiles(applicationDatabasePath)
	if e != nil {
		return nil, applicationPathError(e)
	}
	if guard == nil {
		return nil, fail("APPLICATION_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if e := guard.Close(); e != nil {
			result, failure = nil, applicationPathError(e)
		}
	}()
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(applicationDatabasePath)))
	if err != nil {
		return nil, fail("APPLICATION_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if err := db.Close(); err != nil {
			result, failure = nil, fail("APPLICATION_CLEANUP_FAILED")
		}
	}()
	return snapshotQuery(ctx, db, search)
}

func snapshotQuery(ctx context.Context, db *sql.DB, search string) (*applications.ResumeManagementSnapshot, error) {
	result, e := applications.ReadResumeManagementSnapshot(ctx, db, search)
	if e != nil {
		return nil, applicationReadError(e)
	}
	return result, nil
}

type catalogWitness struct {
	reader *runtimepaths.Root
	paths  map[string]catalogFile
}
type catalogFile struct {
	content []byte
	present bool
}

func (w *catalogWitness) Check(path string) *runtimepaths.Error { return w.reader.Check(path) }
func (w *catalogWitness) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	content, present, e := w.reader.ReadFile(path, limit)
	if e == nil {
		w.paths[path] = catalogFile{append([]byte(nil), content...), present}
	}
	return content, present, e
}
func (w *catalogWitness) recheck() bool {
	for path, old := range w.paths {
		content, present, e := w.reader.ReadFile(path, cataloginput.YAMLMaxInputBytes)
		if e != nil || present != old.present || !bytes.Equal(content, old.content) {
			return false
		}
	}
	return true
}

type selectedRun struct {
	id         string
	modified   time.Time
	raw        []byte
	extraction intakeread.Extraction
	workflow   string
}

func readResumeManagement(ctx context.Context, dataRoot string, data, resources *runtimepaths.Root, filters applications.ResumeFilters) (payload []ManagedResumeEntry, failure error) {
	return readResumeManagementWithFaults(ctx, dataRoot, data, resources, filters, nil)
}

// managementFaults is a package-private deterministic fixture seam. Production
// passes nil; callbacks never own or bypass native path/database guards.
type managementFaults struct {
	afterFirst, afterCatalog, afterSecond func()
	closeRun                              func() error
}

func readResumeManagementWithFaults(ctx context.Context, dataRoot string, data, resources *runtimepaths.Root, filters applications.ResumeFilters, faults *managementFaults) (payload []ManagedResumeEntry, failure error) {
	first, err := managementSnapshot(ctx, data, dataRoot, filters.Search)
	if err != nil {
		return nil, err
	}
	if faults != nil && faults.afterFirst != nil {
		faults.afterFirst()
	}
	resourceWitness := &catalogWitness{resources, map[string]catalogFile{}}
	dataWitness := &catalogWitness{data, map[string]catalogFile{}}
	state, ce := catalogstore.ReadState(resourceWitness, dataWitness)
	if ce != nil || state == nil || !hasCatalogInputs(resourceWitness, catalogstore.CoreGroupsPath, catalogstore.CoreSkillsPath) || !hasCatalogInputs(dataWitness, catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath) {
		return nil, fail("RESUME_MANAGEMENT_CATALOG_FAILED")
	}
	if faults != nil && faults.afterCatalog != nil {
		faults.afterCatalog()
	}
	requested := map[string]bool{}
	for _, row := range first.Rows {
		id, ok := resumeRowID(row)
		if !ok || row.ResumeManagement == nil {
			return nil, fail("RESUME_MANAGEMENT_DATABASE_VALUE")
		}
		identity, exists := first.Identities[id]
		if !exists || identity.SHA256 == "" {
			return nil, fail("RESUME_MANAGEMENT_DATABASE_VALUE")
		}
		requested[identity.SHA256] = true
	}
	selected := map[string]selectedRun{}
	var runsRoot *runtimepaths.Root
	var observed *runtimepaths.ObservedRuns
	defer func() {
		if runsRoot != nil {
			closeErr := runsRoot.Close()
			if faults != nil && faults.closeRun != nil && faults.closeRun() != nil {
				closeErr = fail("RESUME_MANAGEMENT_CLEANUP_FAILED")
			}
			if closeErr != nil {
				payload, failure = nil, fail("RESUME_MANAGEMENT_CLEANUP_FAILED")
			}
		}
	}()
	var present bool
	if len(requested) != 0 {
		var pe *runtimepaths.Error
		present, pe = data.Exists(resumeRunsPath)
		if pe != nil {
			return nil, fail("RESUME_MANAGEMENT_RUNS_REJECTED")
		}
		if present {
			runs, e := runtimepaths.Open(filepath.Join(dataRoot, filepath.FromSlash(resumeRunsPath)))
			if e != nil {
				return nil, fail("RESUME_MANAGEMENT_RUNS_REJECTED")
			}
			runsRoot = runs
			observed, err = scanManagementRuns(ctx, runs, requested, selected)
			if err != nil {
				return nil, err
			}
		}
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, fail("APPLICATION_READ_CANCELED")
	}
	second, err := managementSnapshot(ctx, data, dataRoot, filters.Search)
	if err != nil {
		return nil, err
	}
	if faults != nil && faults.afterSecond != nil {
		faults.afterSecond()
	}
	if !reflect.DeepEqual(first, second) {
		return nil, fail("RESUME_MANAGEMENT_DATABASE_CHANGED")
	}
	if !resourceWitness.recheck() || !dataWitness.recheck() {
		return nil, fail("RESUME_MANAGEMENT_CATALOG_CHANGED")
	}
	result := make([]ManagedResumeEntry, 0, len(first.Rows))
	for _, row := range first.Rows {
		id, ok := resumeRowID(row)
		if !ok || row.ResumeManagement == nil {
			return nil, fail("RESUME_MANAGEMENT_DATABASE_VALUE")
		}
		identity := first.Identities[id]
		entry := ManagedResumeEntry{ResumeEntry: row, DigestSupported: strings.EqualFold(filepath.Ext(identity.FileName), ".txt") || strings.EqualFold(filepath.Ext(identity.FileName), ".docx")}
		run, ok := selected[identity.SHA256]
		if ok {
			entry.DigestSourceID = &run.id
			processed := pythonUTC(run.modified)
			entry.DigestProcessedAt = &processed
		}
		entry.DigestStatus = intakeread.DigestStatus(ok, run.workflow, run.extraction.RegistryRevision, state.Snapshot.CatalogRevision(), run.extraction.ExtractorVersion, intakeextract.ExtractorVersion)
		result = append(result, entry)
	}
	encoded, e := json.Marshal(result)
	if e != nil || len(encoded) > 4<<20 {
		return nil, fail("RESUME_MANAGEMENT_RESULT_LIMIT")
	}
	if len(requested) == 0 {
		// Source indexing does not inspect runs for an empty requested hash set.
	} else if present {
		if e := observed.Recheck(); e != nil {
			return nil, fail("RESUME_MANAGEMENT_RUNS_CHANGED")
		}
	} else {
		stillAbsent, e := data.Exists(resumeRunsPath)
		if e != nil || stillAbsent {
			return nil, fail("RESUME_MANAGEMENT_RUNS_CHANGED")
		}
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, fail("APPLICATION_READ_CANCELED")
	}
	return result, nil
}

func hasCatalogInputs(w *catalogWitness, first, second string) bool {
	if len(w.paths) != 2 {
		return false
	}
	_, a := w.paths[first]
	_, b := w.paths[second]
	return a && b
}

func resumeRowID(row applications.ResumeEntry) (string, bool) {
	raw, err := row.ID.MarshalJSON()
	if err != nil {
		return "", false
	}
	var id string
	if json.Unmarshal(raw, &id) != nil || id == "" {
		return "", false
	}
	return id, true
}

func scanManagementRuns(ctx context.Context, runs *runtimepaths.Root, requested map[string]bool, selected map[string]selectedRun) (*runtimepaths.ObservedRuns, error) {
	observed, e := runs.ObserveRunDirectories()
	if e != nil {
		return nil, fail("RESUME_MANAGEMENT_RUNS_REJECTED")
	}
	var total int64
	read := func(id, leaf string) (runtimepaths.ObservedFile, error) {
		file, e := observed.ReadChildFile(id, leaf, cataloginput.YAMLMaxInputBytes)
		if e != nil {
			return runtimepaths.ObservedFile{}, fail("RESUME_MANAGEMENT_REPORT_REJECTED")
		}
		total += int64(len(file.Content))
		if total > maxResumeReportBytes {
			return runtimepaths.ObservedFile{}, fail("RESUME_MANAGEMENT_REPORT_LIMIT")
		}
		return file, nil
	}
	names := observed.Names()
	for offset := 0; offset < len(names); {
		if ctx == nil || ctx.Err() != nil {
			return nil, fail("APPLICATION_READ_CANCELED")
		}
		// Reserve a full leaf for every concurrent read until its ordered
		// result is consumed. Below one reservation, use the existing
		// sequential path, which can report the aggregate boundary exactly.
		capacity := int((maxResumeReportBytes - total) / cataloginput.YAMLMaxInputBytes)
		width := min(4, len(names)-offset, capacity)
		if width == 0 {
			width = 1
		}
		type extractionRead struct {
			file runtimepaths.ObservedFile
			err  error
		}
		results := make([]extractionRead, width)
		var workers sync.WaitGroup
		for i := range results {
			if width == 1 {
				file, e := observed.ReadChildFile(names[offset+i], "extracted.yaml", cataloginput.YAMLMaxInputBytes)
				results[i].file = file
				if e != nil {
					results[i].err = fail("RESUME_MANAGEMENT_REPORT_REJECTED")
				}
				continue
			}
			workers.Add(1)
			go func(i int) {
				defer workers.Done()
				file, e := observed.ReadChildFile(names[offset+i], "extracted.yaml", cataloginput.YAMLMaxInputBytes)
				results[i].file = file
				if e != nil {
					results[i].err = fail("RESUME_MANAGEMENT_REPORT_REJECTED")
				}
			}(i)
		}
		workers.Wait()
		for i, result := range results {
			if ctx == nil || ctx.Err() != nil {
				return nil, fail("APPLICATION_READ_CANCELED")
			}
			if result.err != nil {
				return nil, fail("RESUME_MANAGEMENT_REPORT_REJECTED")
			}
			id, file := names[offset+i], result.file
			total += int64(len(file.Content))
			if total > maxResumeReportBytes {
				return nil, fail("RESUME_MANAGEMENT_REPORT_LIMIT")
			}
			if !file.Present {
				continue
			}
			extraction, parseErr := intakeread.ParseExtraction(file.Content)
			if parseErr != nil || extraction.Source.ID != id || !requested[extraction.Source.SHA256] {
				continue
			}
			old, exists := selected[extraction.Source.SHA256]
			if !exists || file.Modified.After(old.modified) || file.Modified.Equal(old.modified) && id < old.id {
				selected[extraction.Source.SHA256] = selectedRun{id: id, modified: file.Modified, raw: file.Content, extraction: extraction}
			}
		}
		offset += width
	}
	for hash, run := range selected {
		approval, err := read(run.id, "approval_result.yaml")
		if err != nil {
			return nil, err
		}
		input := intakeread.WorkflowInput{ExtractionRaw: run.raw, ApprovalPresent: approval.Present, ApprovalRaw: approval.Content}
		if approval.Present {
			run.workflow = approvalWorkflow(run.id, input)
		} else {
			review, err := read(run.id, "review.yaml")
			if err != nil {
				return nil, err
			}
			input.ReviewPresent, input.ReviewRaw = review.Present, review.Content
			run.workflow = intakeread.ReviewWorkflow(input).Status
		}
		run.raw = nil
		selected[hash] = run
	}
	return observed, nil
}

func approvalWorkflow(runID string, input intakeread.WorkflowInput) string {
	parsed, pe := intakeread.ParseApproval(input.ApprovalRaw)
	if pe == nil && parsed.SourceID != runID {
		return "review_error"
	}
	return intakeread.ReviewWorkflow(input).Status
}

func pythonUTC(t time.Time) string {
	seconds := float64(t.Unix()) + float64(t.Nanosecond())/1e9
	u := time.UnixMicro(int64(math.RoundToEven(seconds * 1e6))).UTC()
	if u.Nanosecond()/1000 == 0 {
		return u.Format("2006-01-02T15:04:05+00:00")
	}
	return u.Format("2006-01-02T15:04:05.000000+00:00")
}
