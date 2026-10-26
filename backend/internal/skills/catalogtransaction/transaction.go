// Package catalogtransaction publishes private catalogs alone or alongside one
// canonical evidence or Resume approval, through closed (not configurable) policies.
// A durable intent precedes every file change. Incomplete work remains blocked
// for explicit roll-forward recovery; it never attempts silent best-effort undo.
package catalogtransaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const JournalPath = datahome.RegistryRecoveryPath
const MaxJournalBytes = 12 << 20

var ErrInput = errors.New("catalog transaction input is invalid")
var ErrJournal = errors.New("catalog transaction journal is invalid")
var ErrPending = errors.New("catalog transaction recovery is required")
var ErrConflict = errors.New("catalog transaction source changed")
var ErrIO = errors.New("catalog transaction storage failed")
var ErrTarget = errors.New("catalog transaction target is invalid")

type Change struct {
	Path          string `json:"path"`
	BeforePresent bool   `json:"before_present"`
	Before        []byte `json:"before"`
	After         []byte `json:"after"`
	// CheckOnly binds an unchanged second private catalog, including absence.
	// Recovery must not silently accept an external edit to that input.
	CheckOnly bool `json:"check_only,omitempty"`
	// RecordID binds the canonical member in evidence v2, or the final result's
	// source ID in approval v3. Other members and registry v1 omit it.
	RecordID string `json:"record_id,omitempty"`
}
type intent struct {
	Version int      `json:"version"`
	HomeID  string   `json:"data_home_id"`
	Changes []Change `json:"changes"`
	Guards  []Guard  `json:"guards,omitempty"`
}
type envelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}

// Report contains observations, not proof of global absence. Retain it on
// errors: a false IntentRecorded on an early failure does not prove no pending
// journal exists. A recorded intent remains pending unless JournalRemoved.
// FilesComplete is not cleanup; Completed means both after-state verification
// and successful removal of this exact intent. AppliedFiles counts this call,
// not earlier calls interrupted before recovery.
type Report struct {
	IntentRecorded bool
	AppliedFiles   []string
	FilesComplete  bool
	JournalRemoved bool
	Completed      bool
}
type storage interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
	RequireProcessOwnership() *runtimepaths.Error
	EnsureDirectory(string) (bool, *runtimepaths.Error)
	CreateFile(string, []byte) (bool, *runtimepaths.Error)
	ReplaceFile(string, []byte, []byte) (bool, *runtimepaths.Error)
	RemoveFile(string, []byte) (bool, *runtimepaths.Error)
}
type Validate func(context.Context, []Change) error

func stopped(ctx context.Context) error {
	if ctx == nil {
		return ErrInput
	}
	return ctx.Err()
}
func valid(homeID string, changes []Change) bool {
	if len(homeID) != 35 || homeID[:3] != "dh_" {
		return false
	}
	if raw, err := hex.DecodeString(homeID[3:]); err != nil || len(raw) != 16 {
		return false
	}
	if len(changes) < 1 || len(changes) > 2 {
		return false
	}
	seen := map[string]bool{}
	mutates := false
	for _, change := range changes {
		if change.RecordID != "" || (change.Path != catalogstore.CustomGroupsPath && change.Path != catalogstore.CustomSkillsPath) || seen[change.Path] {
			return false
		}
		seen[change.Path] = true
		if len(change.Before) > cataloginput.YAMLMaxInputBytes || len(change.After) > cataloginput.YAMLMaxInputBytes || !change.BeforePresent && len(change.Before) != 0 {
			return false
		}
		if change.CheckOnly && !bytes.Equal(change.Before, change.After) {
			return false
		}
		mutates = mutates || !change.CheckOnly
	}
	return mutates
}
func encode(homeID string, changes []Change) ([]byte, error) {
	return encodeIntent(intent{Version: 1, HomeID: homeID, Changes: changes})
}
func encodeIntent(data intent) ([]byte, error) {
	if !validIntent(data) {
		return nil, ErrInput
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, ErrInput
	}
	hash := sha256.Sum256(payload)
	raw, err := json.Marshal(envelope{payload, hex.EncodeToString(hash[:])})
	_, limit, _ := policy(data.Version)
	if err != nil || len(raw) > limit {
		return nil, ErrInput
	}
	return raw, nil
}
func canonical(raw []byte, target any) bool {
	return canonicalFor(raw, target, MaxJournalBytes, 128)
}
func canonicalFor(raw []byte, target any, limit, maxNodes int) bool {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return false
	}
	depth, nodes, quoted, escaped := 0, 0, false, false
	for _, b := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			nodes++
		case '}', ']':
			depth--
		case ',', ':':
			nodes++
		}
		if depth < 0 || depth > 16 || nodes > maxNodes {
			return false
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return false
	}
	encoded, err := json.Marshal(target)
	return err == nil && bytes.Equal(encoded, raw)
}
func decode(raw []byte, homeID string) ([]Change, error) {
	data, err := decodeIntent(raw, homeID, 1)
	if err != nil {
		return nil, err
	}
	return data.Changes, nil
}
func decodeIntent(raw []byte, homeID string, version int) (intent, error) {
	_, limit, nodes := policy(version)
	if limit == 0 {
		return intent{}, ErrJournal
	}
	var wrapped envelope
	if !canonicalFor(raw, &wrapped, limit, nodes) {
		return intent{}, ErrJournal
	}
	hash := sha256.Sum256(wrapped.Payload)
	if wrapped.SHA256 != hex.EncodeToString(hash[:]) {
		return intent{}, ErrJournal
	}
	var data intent
	if !canonicalFor(wrapped.Payload, &data, limit, nodes) || data.Version != version || data.HomeID != homeID || !validIntent(data) {
		return intent{}, ErrJournal
	}
	return data, nil
}

// BeginOwned and RecoverOwned require the existing native owner. The enclosing
// domain service holds operationgate through these calls and its final rechecks.
// The validator must prove the complete prospective catalog on current resources.
func BeginOwned(ctx context.Context, root *runtimepaths.Root, homeID string, changes []Change, validate Validate) (Report, error) {
	if err := stopped(ctx); err != nil {
		return Report{}, err
	}
	if !valid(homeID, changes) || validate == nil {
		return Report{}, ErrInput
	}
	if info, e := datahome.InspectOwnedForRegistryRecovery(root, nil); e != nil || info.DataHomeID != homeID {
		return Report{}, ErrInput
	}
	return begin(ctx, root, homeID, changes, validate)
}
func RecoverOwned(ctx context.Context, root *runtimepaths.Root, homeID string, validate Validate) (Report, error) {
	if err := stopped(ctx); err != nil {
		return Report{}, err
	}
	if validate == nil {
		return Report{}, ErrInput
	}
	if info, e := datahome.InspectOwnedForRegistryRecovery(root, nil); e != nil || info.DataHomeID != homeID {
		return Report{}, ErrInput
	}
	return resume(ctx, root, homeID, validate)
}
func observe(root storage, change Change) (before, after bool, err error) {
	raw, present, e := root.ReadFile(change.Path, cataloginput.YAMLMaxInputBytes)
	if e != nil || len(raw) > cataloginput.YAMLMaxInputBytes || !present && len(raw) != 0 {
		return false, false, ErrIO
	}
	afterPresent := true
	if change.CheckOnly {
		afterPresent = change.BeforePresent
	}
	return present == change.BeforePresent && bytes.Equal(raw, change.Before), present == afterPresent && bytes.Equal(raw, change.After), nil
}
func begin(ctx context.Context, root storage, homeID string, changes []Change, validate Validate) (report Report, problem error) {
	return beginIntent(ctx, root, intent{Version: 1, HomeID: homeID, Changes: changes}, validate)
}
func beginIntent(ctx context.Context, root storage, data intent, validate Validate) (report Report, problem error) {
	if err := stopped(ctx); err != nil {
		return report, err
	}
	if root == nil || validate == nil || root.RequireProcessOwnership() != nil {
		return report, ErrInput
	}
	raw, err := encodeIntent(data)
	if err != nil {
		return report, err
	}
	journalPath, limit, _ := policy(data.Version)
	old, present, e := root.ReadFile(journalPath, int64(limit))
	if e != nil || len(old) > limit || !present && len(old) != 0 {
		return report, ErrIO
	}
	if present {
		return report, ErrPending
	}
	// Freeze caller-owned slices before validation/publication. The validator
	// borrows them read-only and must not call another gate-acquiring service.
	data, err = decodeIntent(raw, data.HomeID, data.Version)
	if err != nil {
		return report, err
	}
	for _, change := range data.Changes {
		before, _, err := observe(root, change)
		if err != nil {
			return report, err
		}
		if !before {
			return report, ErrConflict
		}
	}
	if err = checkGuards(ctx, root, data.Guards); err != nil {
		return report, err
	}
	if err = validate(ctx, data.Changes); err != nil {
		if e := stopped(ctx); e != nil {
			return report, e
		}
		return report, ErrTarget
	}
	if encoded, e := encodeIntent(data); e != nil || !bytes.Equal(raw, encoded) {
		return report, ErrInput
	}
	for _, change := range data.Changes {
		before, _, err := observe(root, change)
		if err != nil {
			return report, err
		}
		if !before {
			return report, ErrConflict
		}
	}
	if err = checkGuards(ctx, root, data.Guards); err != nil {
		return report, err
	}
	if err = stopped(ctx); err != nil {
		return report, err
	}
	// Intent creation is exclusive; errors can still report that it was saved.
	report.IntentRecorded, e = root.CreateFile(journalPath, raw)
	if e != nil || !report.IntentRecorded {
		return report, ErrIO
	}
	return applyIntent(ctx, root, raw, data, report)
}
func resume(ctx context.Context, root storage, homeID string, validate Validate) (report Report, problem error) {
	return resumePolicy(ctx, root, homeID, 1, validate)
}
func resumePolicy(ctx context.Context, root storage, homeID string, version int, validate Validate) (report Report, problem error) {
	if err := stopped(ctx); err != nil {
		return report, err
	}
	if root == nil || validate == nil || root.RequireProcessOwnership() != nil {
		return report, ErrInput
	}
	journalPath, limit, _ := policy(version)
	if limit == 0 {
		return report, ErrInput
	}
	raw, present, e := root.ReadFile(journalPath, int64(limit))
	if e != nil || len(raw) > limit || !present && len(raw) != 0 {
		return report, ErrIO
	}
	if !present {
		return report, nil
	}
	report.IntentRecorded = true
	data, err := decodeIntent(raw, homeID, version)
	if err != nil {
		return report, err
	}
	// Check every leaf before performing another write, never overwrite a
	// third state just because an intent carries different desired bytes.
	for _, change := range data.Changes {
		before, after, err := observe(root, change)
		if err != nil {
			return report, err
		}
		if !before && !after {
			return report, ErrConflict
		}
	}
	if err = checkGuards(ctx, root, data.Guards); err != nil {
		return report, err
	}
	if err = validate(ctx, data.Changes); err != nil {
		if e := stopped(ctx); e != nil {
			return report, e
		}
		return report, ErrTarget
	}
	if encoded, e := encodeIntent(data); e != nil || !bytes.Equal(raw, encoded) {
		return report, ErrInput
	}
	return applyIntent(ctx, root, raw, data, report)
}
func applyIntent(ctx context.Context, root storage, journal []byte, data intent, report Report) (Report, error) {
	journalPath, limit, _ := policy(data.Version)
	for _, change := range data.Changes {
		if err := stopped(ctx); err != nil {
			return report, err
		}
		held, present, e := root.ReadFile(journalPath, int64(limit))
		if e != nil || !present || !bytes.Equal(held, journal) {
			return report, ErrJournal
		}
		before, after, err := observe(root, change)
		if err != nil {
			return report, err
		}
		if after {
			continue
		}
		if !before {
			return report, ErrConflict
		}
		if err := checkGuards(ctx, root, data.Guards); err != nil {
			return report, err
		}
		var committed bool
		if change.BeforePresent {
			committed, e = root.ReplaceFile(change.Path, change.Before, change.After)
		} else {
			// Only catalogs may create parents. Approval's source directory must
			// already contain the two guarded evidence files; never recreate it.
			if change.Path == catalogstore.CustomGroupsPath || change.Path == catalogstore.CustomSkillsPath {
				for _, directory := range []string{"career_vault", "career_vault/taxonomy"} {
					if _, e := root.EnsureDirectory(directory); e != nil {
						return report, ErrIO
					}
				}
			}
			if err := stopped(ctx); err != nil {
				return report, err
			}
			committed, e = root.CreateFile(change.Path, change.After)
		}
		if committed {
			report.AppliedFiles = append(report.AppliedFiles, change.Path)
		}
		if e != nil || !committed {
			return report, ErrIO
		}
	}
	// Synchronous final verification/cleanup is not abandoned after the last
	// write due to a late request timeout. Report facts, not an invented undo.
	for _, change := range data.Changes {
		_, after, err := observe(root, change)
		if err != nil {
			return report, err
		}
		if !after {
			return report, ErrConflict
		}
	}
	// Final synchronous verification must not pretend a late cancellation
	// rolled back the completed writes, but drift still leaves recovery blocked.
	if err := checkGuards(context.Background(), root, data.Guards); err != nil {
		return report, err
	}
	report.FilesComplete = true
	removed, e := root.RemoveFile(journalPath, journal)
	report.JournalRemoved = removed
	report.Completed = removed && e == nil
	if e != nil || !removed {
		return report, ErrIO
	}
	return report, nil
}
