package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
	"careerhub.local/backend/internal/textcompat"
)

const normalizedPath = "jobs/normalized"

type normalizedReads struct {
	root    *runtimepaths.Root
	path    string
	names   []string
	present bool
	absent  []string
}

func normalizedNames(root *runtimepaths.Root, absolute string) (names []string, present bool, problem *Error) {
	present, pe := root.Exists(normalizedPath)
	if pe != nil {
		return nil, false, failed("source", pe.Code)
	}
	if !present {
		return []string{}, false, nil
	}
	dir, pe := runtimepaths.Open(absolute)
	if pe != nil {
		return nil, false, failed("source", pe.Code)
	}
	defer func() {
		if dir.Close() != nil && problem == nil {
			names, problem = nil, failed("cleanup", "APPLICATION_LEDGER_DIRECTORY_CLOSE_FAILED")
		}
	}()
	names, pe = dir.ListNames()
	if pe != nil {
		return nil, false, failed("source", pe.Code)
	}
	// The supported Python baseline is WindowsPath: case-insensitive .yaml
	// glob, ordered by Unicode lower rather than casefold or filesystem order.
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(textcompat.Lower(a), textcompat.Lower(b)) })
	return names, true, nil
}

func (r *normalizedReads) unchanged() *Error {
	names, present, e := normalizedNames(r.root, r.path)
	if e != nil || present != r.present || !slices.Equal(names, r.names) {
		return failed("source", "APPLICATION_LEDGER_SOURCE_CHANGED")
	}
	for _, path := range r.absent {
		if exists, e := r.root.Exists(path); e != nil || exists {
			return failed("source", "APPLICATION_LEDGER_SOURCE_CHANGED")
		}
	}
	return nil
}

type normalizedJobs struct {
	fingerprints map[string]string
	invalid      []string
	invalidIDs   map[string]bool
	inputs       []map[string]any
	rawPaths     map[string]string
}

func cardField(card cataloginput.Value, key string) cataloginput.Value {
	for _, pair := range card.Entries {
		if pair.Key.Kind == "string" && pair.Key.Text == key {
			return pair.Value
		}
	}
	return cataloginput.Value{Kind: "null"}
}

// loadNormalized deliberately does not digest, rewrite, delete or register any
// source. Report mode records invalid YAML/schema IDs; unsafe I/O/limit failures
// stop the operation, since they are not evidence that a JD is absent.
func (s *Service) loadNormalized(reads *sourceReads, strict bool) (*normalizedJobs, *Error) {
	abs := filepath.Join(s.root, "jobs", "normalized")
	names, present, e := normalizedNames(reads.root, abs)
	if e != nil {
		return nil, e
	}
	reads.normalizedReads = &normalizedReads{root: reads.root, path: abs, names: names, present: present}
	result := &normalizedJobs{fingerprints: map[string]string{}, invalid: []string{}, invalidIDs: map[string]bool{}, inputs: []map[string]any{}, rawPaths: map[string]string{}}
	for _, name := range names {
		if reads.ctx.Err() != nil {
			return nil, canceled()
		}
		if !strings.HasSuffix(textcompat.Lower(name), ".yaml") {
			continue
		}
		stem := name
		if len(name) > 5 {
			stem = name[:len(name)-5]
		}
		raw, present, pe := reads.ReadFile(normalizedPath+"/"+name, cataloginput.YAMLMaxInputBytes)
		if pe != nil {
			if pe.Code == "FILE_SIZE_LIMIT" {
				return nil, failed("source", "APPLICATION_MAINTENANCE_INPUT_LIMIT")
			}
			return nil, failed("source", pe.Code)
		}
		if !present {
			return nil, failed("source", "APPLICATION_LEDGER_SOURCE_CHANGED")
		}
		card, parseErr := cataloginput.ParseYAML(raw)
		if parseErr != nil && parseErr.Kind != "invalid" {
			// Hitting an adapter bound or unsupported YAML feature is not proof
			// that the user's card is malformed. Do not publish partial counts.
			return nil, failed("source", parseErr.Code)
		}
		valid := parseErr == nil && card.Kind == "mapping"
		if valid {
			report, validation := jobcard.Validate(reads.ctx, card, func(ctx context.Context, path string) (bool, *jobcard.Error) {
				_, exists, e := reads.ReadFile(path, maxRawBytes)
				if e != nil {
					if e.Code == "FILE_SIZE_LIMIT" {
						return false, &jobcard.Error{Code: "JOB_CARD_INPUT_LIMIT"}
					}
					return false, &jobcard.Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
				}
				return exists, nil
			})
			if validation != nil {
				return nil, failed("source", validation.Code)
			}
			valid = report.SchemaValid()
		}
		if !valid {
			if strict {
				return nil, invalid("Normalized Job Card could not be validated")
			}
			result.invalid = append(result.invalid, stem)
			id := stem
			if parseErr == nil && card.Kind == "mapping" {
				if stored := cardField(card, "id"); stored.Kind == "string" && stored.Text != "" {
					id = stored.Text
				}
			}
			result.invalidIDs[id] = true
			continue
		}
		id, fp, rawPath := cardField(card, "id").Text, cardField(card, "content_fingerprint").Text, cardField(card, "raw_jd_path").Text
		if fp == "" {
			raw, exists, pe := reads.ReadFile(rawPath, maxRawBytes)
			if pe != nil || !exists {
				return nil, failed("source", "APPLICATION_MAINTENANCE_SOURCE_UNAVAILABLE")
			}
			fp, e = fingerprintBytes(raw)
			if e != nil {
				return nil, e
			}
		}
		if _, duplicate := result.fingerprints[id]; duplicate {
			result.invalid = append(result.invalid, stem)
		} else {
			result.fingerprints[id], result.rawPaths[id] = fp, rawPath
		}
		// Report fallback and registration are distinct source contracts: a
		// legacy missing hash registers from raw text, but an explicit null is
		// rejected by the registration validator rather than silently repaired.
		input := map[string]any{"id": id, "raw_jd_path": rawPath}
		for _, pair := range card.Entries {
			if pair.Key.Kind == "string" && pair.Key.Text == "content_fingerprint" {
				if pair.Value.Kind == "string" {
					input["content_fingerprint"] = pair.Value.Text
				} else {
					input["content_fingerprint"] = nil
				}
			}
		}
		for _, key := range []string{"company", "title", "source_url"} {
			input[key] = cardField(card, key).Text
		}
		result.inputs = append(result.inputs, input)
	}
	return result, nil
}

// SyncNormalizedJobs atomically registers all validated cards. It retains input
// order/duplicate-ID behavior and workflow metadata, and never sets Applied.
func (s *Service) SyncNormalizedJobs(ctx context.Context) (*Outcome, *Error) {
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		cards, e := s.loadNormalized(reads, true)
		if e != nil {
			return nil, e
		}
		jobs, e := snapshots(cards.inputs, reads)
		if e != nil {
			return nil, e
		}
		now := s.now()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, e := applications.RegisterInTransaction(ctx, tx, jobs, now)
			if e != nil {
				return nil, applicationProblem(e, "")
			}
			result.Result, _ = json.Marshal(len(jobs))
			return result, nil
		}, nil
	})
}

// RepairExactOrphan is an explicit maintenance action after reviewing a report
// and backup. A transport must not expose this as an automatic deduplication.
func (s *Service) RepairExactOrphan(ctx context.Context, orphanID, survivorID string, confirmed bool) (*Outcome, *Error) {
	for _, id := range []string{orphanID, survivorID} {
		if e := boundedText(id, applications.MaxIDBytes); e != nil {
			return emptyFailure(e)
		}
		runes := []rune(id)
		if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") || len(runes) > 1 && runes[1] == ':' {
			return emptyFailure(invalid("Repair identities must be plain non-empty file names"))
		}
	}
	if orphanID == survivorID {
		return emptyFailure(invalid("orphan and survivor job ids must differ"))
	}
	if !confirmed {
		return emptyFailure(failed("confirmation", "APPLICATION_REPAIR_CONFIRMATION_REQUIRED"))
	}
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		cards, e := s.loadNormalized(reads, false)
		if e != nil {
			return nil, e
		}
		if _, exists := cards.fingerprints[orphanID]; exists {
			return nil, invalid("orphan job is not an unlinked ledger record")
		}
		fp, exists := cards.fingerprints[survivorID]
		if !exists {
			return nil, invalid("survivor job must have a normalized JD")
		}
		if len(cards.invalid) != 0 {
			return nil, failed("repair", "APPLICATION_REPAIR_INVALID_SOURCE_INVENTORY")
		}
		// Material/text stores are filesystem relationships, not SQLite FKs.
		// Even an empty existing job directory needs explicit recovery review.
		for _, parent := range []string{"outputs/application_materials/", "outputs/application_texts/", "outputs/application_packages/"} {
			path := parent + orphanID
			present, pe := reads.root.Exists(path)
			if pe != nil {
				return nil, failed("source", pe.Code)
			}
			if present {
				return nil, failed("repair", "REPAIR_DURABLE_DATA")
			}
			reads.normalizedReads.absent = append(reads.normalizedReads.absent, path)
		}
		// Do not let a stale, explicitly stored hash authorize deletion.
		raw, present, pe := reads.ReadFile(cards.rawPaths[survivorID], maxRawBytes)
		if pe != nil || !present {
			return nil, failed("source", "APPLICATION_MAINTENANCE_SOURCE_UNAVAILABLE")
		}
		actual, e := fingerprintBytes(raw)
		if e != nil || actual != fp {
			return nil, failed("repair", "REPAIR_SOURCE_MISMATCH")
		}
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, e := applications.RepairExactOrphanInTransaction(ctx, tx, orphanID, survivorID, fp)
			return result, applicationProblem(e, "")
		}, nil
	})
}

// IntegrityReport is genuinely read-only, including on a missing database.
// It retains the same process gate/ownership and guarded RO SQLite boundary as
// the other migrated read services; no schema upgrade or implicit repair occurs.
func (s *Service) IntegrityReport(ctx context.Context) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || s.checkpoint == nil || s.closeDB == nil || s.closeRoot == nil || ctx == nil {
		return out, failed("configuration", "APPLICATION_LEDGER_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, canceled()
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", pe.Code)
	}
	var db *sql.DB
	var pins *runtimepaths.SQLiteFiles
	defer func() {
		cleanup := func(code string) {
			out.CleanupFailed, out.CleanupCode = true, code
			if problem == nil {
				problem = failed("cleanup", code)
			}
		}
		if db != nil && s.closeDB(db) != nil {
			cleanup("APPLICATION_LEDGER_DATABASE_CLOSE_FAILED")
		}
		if pins != nil && pins.Close() != nil {
			cleanup("APPLICATION_LEDGER_FILES_CLOSE_FAILED")
		}
		if s.closeRoot(root) != nil {
			cleanup("APPLICATION_LEDGER_ROOT_CLOSE_FAILED")
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	home, problem := s.ready(root, nil)
	if problem != nil {
		return out, problem
	}
	reads := &sourceReads{ctx: ctx, root: root, home: *home}
	cards, problem := s.loadNormalized(reads, false)
	if problem != nil {
		return out, problem
	}
	pins, pe = root.GuardSQLiteFiles(databasePath)
	if pe != nil {
		return out, failed("path", pe.Code)
	}
	if pins != nil {
		var err error
		db, err = sqlitestore.OpenReadOnly(ctx, filepath.Join(s.root, filepath.FromSlash(databasePath)))
		if err != nil {
			return out, failed("open", "DATABASE_OPEN_FAILED")
		}
	}
	if problem = s.checkpoint("prepared"); problem != nil {
		return out, problem
	}
	report, ae := applications.ReadIntegrity(ctx, db, cards.fingerprints, cards.invalid)
	if ae != nil {
		return out, applicationProblem(ae, "")
	}
	if problem = s.checkpoint("applied"); problem != nil {
		return out, problem
	}
	if problem = reads.unchanged(); problem != nil {
		return out, problem
	}
	if _, problem = s.ready(root, home); problem != nil {
		return out, problem
	}
	if pins != nil {
		if pe = pins.Check(); pe != nil {
			return out, failed("path", pe.Code)
		}
	} else if exists, pe := root.Exists(databasePath); pe != nil || exists {
		return out, failed("source", "APPLICATION_LEDGER_SOURCE_CHANGED")
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	out.Result, _ = json.Marshal(report) // bounded/serializable in applications
	return out, nil
}
