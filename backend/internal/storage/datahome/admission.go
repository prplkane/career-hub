// Package datahome validates explicit Data Home metadata and supplies a separate
// Windows bootstrap use case. Inspect never writes or changes permissions.
package datahome

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

const (
	MarkerPath                  = ".career_hub_data_home.json"
	DocumentRecoveryPath        = "backups/document-encryption-migration/active.json"
	RestoreRecoveryPath         = ".career_hub_restore_journal.json"
	RegistryRecoveryPath        = ".career_hub_registry_transaction.json"
	EvidenceRecoveryPath        = ".career_hub_evidence_transaction.json"
	DiscoveryRecoveryPath       = ".career_hub_discovery_transaction.json"
	ResumeRecoveryPath          = ".career_hub_resume_transaction.json"
	IntakeRecoveryPath          = ".career_hub_intake_transaction.json"
	NewJobRecoveryPath          = ".career_hub_new_job_transaction.json"
	EmailCredentialRecoveryPath = ".career_hub_email_credential_transaction.json"
	ApprovalRecoveryPath        = ".career_hub_approval_transaction.json"
	MaxMarkerBytes              = 64 << 10
	MaxJSONDepth                = 900
)

// Error contains only fixed codes. Unsupported marks a known source-language or
// bounded-input difference; callers must not count that as source-error parity.
type Error struct {
	Code        string
	Unsupported string
}

func (e *Error) Error() string { return "data home admission failed" }
func fail(code string) *Error  { return &Error{Code: code} }
func unsupported(reason string) *Error {
	return &Error{Code: "DATA_HOME_INPUT_UNSUPPORTED", Unsupported: reason}
}

type Info struct {
	FormatVersion int    `json:"format_version"`
	Channel       string `json:"channel"`
	Initialized   bool   `json:"initialized"`
	DataHomeID    string `json:"data_home_id"`
}

type RecoveryCardinality string
type RecoveryKind string
type RecoveryMarkerState string

const (
	RecoveryNone     RecoveryCardinality = "none"
	RecoverySingle   RecoveryCardinality = "single"
	RecoveryMultiple RecoveryCardinality = "multiple"

	RecoveryDocumentMigration RecoveryKind = "document_migration"
	RecoveryRestore           RecoveryKind = "restore"
	RecoveryRegistry          RecoveryKind = "registry"
	RecoveryEvidence          RecoveryKind = "evidence"
	RecoveryDiscovery         RecoveryKind = "discovery"
	RecoveryResume            RecoveryKind = "resume"
	RecoveryIntake            RecoveryKind = "intake"
	RecoveryNewJob            RecoveryKind = "new_job"
	RecoveryEmailCredential   RecoveryKind = "email_credential"
	RecoveryApproval          RecoveryKind = "approval"

	RecoveryMarkerInitialized RecoveryMarkerState = "initialized"
	RecoveryMarkerless        RecoveryMarkerState = "markerless"
)

type RecoveryClassification struct {
	Cardinality RecoveryCardinality
	Kind        RecoveryKind
	Marker      RecoveryMarkerState
}

type recoverySentinel struct {
	kind       RecoveryKind
	path       string
	bypassable bool
}

var orderedRecoverySentinels = [...]recoverySentinel{
	{RecoveryDocumentMigration, DocumentRecoveryPath, false},
	{RecoveryRestore, RestoreRecoveryPath, false},
	{RecoveryRegistry, RegistryRecoveryPath, true},
	{RecoveryEvidence, EvidenceRecoveryPath, true},
	{RecoveryDiscovery, DiscoveryRecoveryPath, true},
	{RecoveryResume, ResumeRecoveryPath, true},
	{RecoveryIntake, IntakeRecoveryPath, true},
	{RecoveryNewJob, NewJobRecoveryPath, true},
	{RecoveryEmailCredential, EmailCredentialRecoveryPath, true},
	{RecoveryApproval, ApprovalRecoveryPath, true},
}

type reader interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
	Exists(string) (bool, *runtimepaths.Error)
}

// Inspect validates a current marker and recovery sentinels in source order.
// An expected channel is optional; a non-nil empty string still means mismatch.
// No markerless/legacy home is initialized or migrated by this operation.
func Inspect(root string, expectedChannel *string) (info *Info, failure *Error) {
	held, pathError := runtimepaths.Open(root)
	if pathError != nil {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if err := held.Close(); err != nil && failure == nil {
			info, failure = nil, fail("DATA_HOME_UNAVAILABLE")
		}
	}()
	return inspect(held, expectedChannel)
}

func inspect(source reader, expectedChannel *string) (*Info, *Error) {
	return inspectRecovery(source, expectedChannel, false)
}

// ClassifyOwnedRecovery inventories only the fixed sentinel paths while
// the dedicated process owns this Home. It reads no sentinel bytes, grants no
// bypass and invokes no recovery owner. Markerless results are informational;
// callers must not dispatch an owner without that owner's normal admission.
func ClassifyOwnedRecovery(root *runtimepaths.Root, expectedChannel *string) (*RecoveryClassification, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return classifyRecovery(root, expectedChannel)
}

func classifyRecovery(source reader, expectedChannel *string) (*RecoveryClassification, *Error) {
	info, marked, problem := readMarker(source)
	if problem != nil {
		return nil, problem
	}
	marker := RecoveryMarkerless
	if marked {
		marker = RecoveryMarkerInitialized
	}
	count := 0
	var kind RecoveryKind
	for _, sentinel := range orderedRecoverySentinels {
		present, pathError := source.Exists(sentinel.path)
		if pathError != nil {
			return nil, fail("DATA_HOME_UNAVAILABLE")
		}
		if present {
			count++
			kind = sentinel.kind
		}
	}
	if info != nil && expectedChannel != nil && info.Channel != *expectedChannel {
		return nil, fail("DATA_HOME_CHANNEL_MISMATCH")
	}
	result := &RecoveryClassification{Cardinality: RecoveryNone, Marker: marker}
	switch count {
	case 1:
		result.Cardinality, result.Kind = RecoverySingle, kind
	case 0:
	default:
		result.Cardinality = RecoveryMultiple
	}
	return result, nil
}

// InspectOwnedForRegistryRecovery is only for the explicit native recovery
// command. It requires the process's existing ownership and bypasses only the
// registry sentinel, never marker/channel/document/restore checks. Ordinary
// readers and writers must use Inspect and refuse an unresolved transaction.
func InspectOwnedForRegistryRecovery(root *runtimepaths.Root, expectedChannel *string) (*Info, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return inspectRecovery(root, expectedChannel, true)
}

func inspectRecovery(source reader, expectedChannel *string, registryRecovery bool) (*Info, *Error) {
	skip := ""
	if registryRecovery {
		skip = RegistryRecoveryPath
	}
	return inspectSkippingRecovery(source, expectedChannel, skip)
}

// InspectOwnedForEvidenceRecovery bypasses only the evidence transaction
// sentinel. Registry/document/restore recovery still blocks it. It never claims
// a home and must only be used by the explicit evidence recovery boundary.
func InspectOwnedForEvidenceRecovery(root *runtimepaths.Root, expectedChannel *string) (*Info, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return inspectSkippingRecovery(root, expectedChannel, EvidenceRecoveryPath)
}

// Only the owned Discovery recovery boundary may bypass its own intent. Other
// pending recovery, marker/channel checks and process ownership remain mandatory.
func InspectOwnedForDiscoveryRecovery(root *runtimepaths.Root, expectedChannel *string) (*Info, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return inspectSkippingRecovery(root, expectedChannel, DiscoveryRecoveryPath)
}

// Resume recovery may bypass only its own pending intent, never another
// migration/restore or the existing native ownership and marker checks.
func InspectOwnedForResumeRecovery(root *runtimepaths.Root, expectedChannel *string) (*Info, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return inspectSkippingRecovery(root, expectedChannel, ResumeRecoveryPath)
}

// Only the owned intake recovery boundary may bypass the intake intent.
// Other recovery sentinels, marker/channel checks and process ownership remain
// mandatory. This does not claim a home or enable an intake writer.
func InspectOwnedForIntakeRecovery(root *runtimepaths.Root, expectedChannel *string) (*Info, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return inspectSkippingRecovery(root, expectedChannel, IntakeRecoveryPath)
}

// New-job recovery may bypass only its own pending raw/card intent. This
// admission helper does not inspect the intent, replay files or enable a writer.
func InspectOwnedForNewJobRecovery(root *runtimepaths.Root, expectedChannel *string) (*Info, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return inspectSkippingRecovery(root, expectedChannel, NewJobRecoveryPath)
}

// Email credential recovery may bypass only its own pending intent. It still
// requires native process ownership and refuses every other recovery sentinel.
func InspectOwnedForEmailCredentialRecovery(root *runtimepaths.Root, expectedChannel *string) (*Info, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return inspectSkippingRecovery(root, expectedChannel, EmailCredentialRecoveryPath)
}

// Approval recovery bypasses only its own multi-file Vault/catalog intent.
// It never claims a Home or authorizes ordinary intake edits during recovery.
func InspectOwnedForApprovalRecovery(root *runtimepaths.Root, expectedChannel *string) (*Info, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	return inspectSkippingRecovery(root, expectedChannel, ApprovalRecoveryPath)
}

func inspectSkippingRecovery(source reader, expectedChannel *string, skip string) (*Info, *Error) {
	info, present, problem := readMarker(source)
	if problem != nil {
		return nil, problem
	}
	if !present {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	for _, sentinel := range orderedRecoverySentinels {
		if sentinel.bypassable && sentinel.path == skip {
			continue
		}
		present, pathError := source.Exists(sentinel.path)
		if pathError != nil {
			return nil, fail("DATA_HOME_UNAVAILABLE")
		}
		if present {
			return nil, fail("DATA_HOME_RECOVERY_REQUIRED")
		}
	}
	if expectedChannel != nil && info.Channel != *expectedChannel {
		return nil, fail("DATA_HOME_CHANNEL_MISMATCH")
	}
	return info, nil
}

func readMarker(source reader) (*Info, bool, *Error) {
	raw, present, pathError := source.ReadFile(MarkerPath, MaxMarkerBytes)
	if pathError != nil {
		switch pathError.Code {
		case "FILE_SIZE_LIMIT":
			return nil, false, unsupported("marker_size")
		case "PATH_NOT_REGULAR", "FILE_OPEN", "FILE_READ", "FILE_STAT":
			return nil, false, fail("INVALID_DATA_HOME")
		default:
			return nil, false, fail("DATA_HOME_UNAVAILABLE")
		}
	}
	if !present {
		return nil, false, nil
	}
	info, e := parseMarker(raw)
	if e != nil {
		return nil, false, e
	}
	return info, true, nil
}

func parseMarker(raw []byte) (*Info, *Error) {
	return parseMarkerMode(raw, false)
}

// ParseMarkerForDocumentMigration is a byte-only parser for the explicit
// maintenance owner. It accepts the exact legacy v1 marker as well as current
// v2, but grants no filesystem, recovery-bypass, DPAPI, or write authority.
// Ordinary admission must continue to use Inspect and reject v1.
func ParseMarkerForDocumentMigration(raw []byte) (*Info, *Error) {
	return parseMarkerMode(raw, true)
}

func parseMarkerMode(raw []byte, allowLegacy bool) (*Info, *Error) {
	if len(raw) > MaxMarkerBytes {
		return nil, unsupported("marker_size")
	}
	if !utf8.Valid(raw) {
		return nil, fail("INVALID_DATA_HOME")
	}
	normalized, limit := normalizeMarkerJSON(raw)
	// Refuse nesting outside the selected input contract before the decoder's
	// own depth ceiling can turn it into a supposed source-domain error. Python
	// can raise an unhandled RecursionError for these inputs instead.
	if limit == "json_depth" {
		return nil, unsupported(limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fail("INVALID_DATA_HOME")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fail("INVALID_DATA_HOME")
	}
	if limit != "" {
		return nil, unsupported(limit)
	}
	payload, ok := value.(map[string]any)
	if !ok || payload["application"] != "CareerHub" {
		return nil, fail("INVALID_DATA_HOME")
	}
	version, ok := markerVersion(payload["format_version"])
	if !ok {
		return nil, fail("INVALID_DATA_HOME")
	}
	channel, ok := payload["channel"].(string)
	if !ok {
		switch payload["channel"].(type) {
		case []any, map[string]any:
			return nil, unsupported("unhashable_channel")
		}
		return nil, fail("INVALID_DATA_HOME")
	}
	if channel != "dev" && channel != "stable" {
		return nil, fail("INVALID_DATA_HOME")
	}
	if version.Cmp(big.NewInt(2)) > 0 {
		return nil, fail("DATA_HOME_NEWER_THAN_APP")
	}
	if allowLegacy && version.Cmp(big.NewInt(1)) == 0 && len(payload) == 3 {
		return &Info{1, channel, true, ""}, nil
	}
	if version.Cmp(big.NewInt(2)) != 0 {
		return nil, fail("DATA_HOME_MIGRATION_REQUIRED")
	}
	id, ok := payload["data_home_id"].(string)
	if len(payload) != 4 || !ok || !validHomeID(id) {
		return nil, fail("INVALID_DATA_HOME")
	}
	return &Info{2, channel, true, id}, nil
}

func validHomeID(id string) bool {
	if len(id) != 35 || !strings.HasPrefix(id, "dh_") {
		return false
	}
	for _, c := range id[3:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func markerVersion(value any) (*big.Int, bool) {
	switch value := value.(type) {
	case bool: // Python isinstance(True, int) is intentionally preserved.
		if value {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			return nil, false
		}
		return new(big.Int).SetString(string(value), 10)
	default:
		return nil, false
	}
}

// These non-JSON constants are floats in Python. Marker validation never uses
// their numeric values, so a float token preserves every relevant type decision,
// including constants overwritten by a later duplicate key. Encoding/json still
// validates all grammar; ordinary duplicate object keys remain source-last-wins.
func normalizeMarkerJSON(raw []byte) ([]byte, string) {
	var out bytes.Buffer
	limit, depth := "", 0
	for i := 0; i < len(raw); {
		start := i
		if raw[i] == '"' {
			i++
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(raw) {
				i = len(raw)
			}
			out.Write(raw[start:i])
		} else if strings.ContainsRune("{}[],: \t\r\n", rune(raw[i])) {
			if raw[i] == '{' || raw[i] == '[' {
				depth++
			}
			if raw[i] == '}' || raw[i] == ']' {
				depth--
			}
			if depth > MaxJSONDepth {
				limit = "json_depth"
			}
			out.WriteByte(raw[i])
			i++
		} else {
			for i < len(raw) && !strings.ContainsRune("{}[],: \t\r\n\"", rune(raw[i])) {
				i++
			}
			token := string(raw[start:i])
			if len(strings.TrimPrefix(token, "-")) > 4300 && !strings.ContainsAny(token, ".eE") && limit == "" {
				limit = "integer_digits"
			}
			if token == "NaN" || token == "Infinity" || token == "-Infinity" {
				out.WriteString("0.0")
			} else {
				out.WriteString(token)
			}
		}
	}
	return out.Bytes(), limit
}
