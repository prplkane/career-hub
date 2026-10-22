package homebackup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"careerhub.local/backend/internal/datahome"
)

const maxRestoreJournalBytes = 16 << 10

// Native version 2 is deliberately incompatible with Python's unscoped v1
// journal. Checksums detect corruption, not hostile same-user forgery. The
// fingerprint must always come from an independent native Root observation.
type restoreJournal struct {
	Version              int           `json:"version"`
	RestoreID            string        `json:"restore_id"`
	RootFingerprint      string        `json:"root_fingerprint"`
	BeforeHome           datahome.Info `json:"before_home"`
	AfterHome            datahome.Info `json:"after_home"`
	BeforeMarkerSHA256   string        `json:"before_marker_sha256"`
	AfterMarkerSHA256    string        `json:"after_marker_sha256"`
	BeforeManifestSHA256 string        `json:"before_manifest_sha256"`
	AfterManifestSHA256  string        `json:"after_manifest_sha256"`
	Roots                []restoreRoot `json:"roots"`
}

type restoreRoot struct {
	Name      string `json:"name"`
	HadActive bool   `json:"had_active"`
}

type restoreEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}

// These bytes are evidence, not capabilities. The orchestration owner must
// still validate destination/staged payloads and stop writers before publishing.
type restoreEvidence struct {
	beforeMarker, afterMarker     []byte
	beforeManifest, afterManifest []byte
}

func (j restoreJournal) recoveryPath() string {
	return "backups/restore-recovery/restore_" + j.RestoreID
}

func (j restoreJournal) stagePath() string { return restorePrefix + j.RestoreID }

func restoreDigest(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func validRestoreHome(home datahome.Info) bool {
	_, p := datahome.MarkerBytes(home.Channel, home.DataHomeID)
	return p == nil && home.FormatVersion == 2 && home.Initialized
}

func validRestoreJournal(j restoreJournal) bool {
	if j.Version != 2 || !lowerHex(j.RestoreID, 32) || !lowerHex(j.RootFingerprint, 64) ||
		!validRestoreHome(j.BeforeHome) || !validRestoreHome(j.AfterHome) || j.BeforeHome.Channel != j.AfterHome.Channel ||
		!lowerHex(j.BeforeMarkerSHA256, 64) || !lowerHex(j.AfterMarkerSHA256, 64) ||
		!lowerHex(j.BeforeManifestSHA256, 64) || !lowerHex(j.AfterManifestSHA256, 64) || j.Roots == nil || len(j.Roots) > 8 {
		return false
	}
	previous, documents := "", false
	for _, root := range j.Roots {
		if !allowedRoot(root.Name) || root.Name <= previous {
			return false
		}
		previous = root.Name
		documents = documents || root.Name == "documents"
	}
	if !documents && (j.BeforeHome != j.AfterHome || j.BeforeMarkerSHA256 != j.AfterMarkerSHA256) {
		return false
	}
	return true
}

func encodeRestoreJournal(j restoreJournal, fingerprint string) ([]byte, *Error) {
	if !validRestoreJournal(j) {
		return nil, failure("BACKUP_RESTORE_JOURNAL_INVALID")
	}
	if !lowerHex(fingerprint, 64) || fingerprint != j.RootFingerprint {
		return nil, failure("BACKUP_RESTORE_SCOPE_MISMATCH")
	}
	payload, err := json.Marshal(j)
	if err != nil {
		return nil, failure("BACKUP_RESTORE_JOURNAL_INVALID")
	}
	raw, err := json.Marshal(restoreEnvelope{Payload: payload, SHA256: restoreDigest(payload)})
	if err != nil || len(raw) > maxRestoreJournalBytes {
		return nil, failure("BACKUP_RESTORE_JOURNAL_INVALID")
	}
	return raw, nil
}

func decodeRestoreJournal(raw []byte, fingerprint string) (restoreJournal, *Error) {
	var envelope restoreEnvelope
	var j restoreJournal
	if isLegacyRestoreJournal(raw) {
		return restoreJournal{}, failure("BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED")
	}
	if len(raw) > maxRestoreJournalBytes || !restoreCanonicalJSON(raw, &envelope) ||
		envelope.SHA256 != restoreDigest(envelope.Payload) || !restoreCanonicalJSON(envelope.Payload, &j) || !validRestoreJournal(j) {
		return restoreJournal{}, failure("BACKUP_RESTORE_JOURNAL_INVALID")
	}
	if !lowerHex(fingerprint, 64) || fingerprint != j.RootFingerprint {
		return restoreJournal{}, failure("BACKUP_RESTORE_SCOPE_MISMATCH")
	}
	return j, nil
}

func restoreCanonicalJSON(raw []byte, target any) bool {
	if len(raw) == 0 || json.Unmarshal(raw, target) != nil {
		return false
	}
	canonical, err := json.Marshal(target)
	return err == nil && bytes.Equal(raw, canonical)
}

func makeRestoreJournal(id, fingerprint string, marker []byte, before, after Manifest, active map[string]bool) (restoreJournal, restoreEvidence, *Error) {
	invalid := func() (restoreJournal, restoreEvidence, *Error) {
		return restoreJournal{}, restoreEvidence{}, failure("BACKUP_RESTORE_EVIDENCE_INVALID")
	}
	before, p := validationManifest(before)
	if p != nil || validatePayloadPaths(before.Files) != nil {
		return invalid()
	}
	after, p = validationManifest(after)
	if p != nil || validatePayloadPaths(after.Files) != nil {
		return invalid()
	}
	home, markerProblem := datahome.ParseMarkerForDocumentMigration(marker)
	if markerProblem != nil || home == nil || !validRestoreHome(*home) || before.DataHomeID != home.DataHomeID ||
		before.DataHomeChannel != home.Channel || after.DataHomeChannel != home.Channel {
		return invalid()
	}
	roots := restoreManifestRoots(after)
	if len(active) != len(roots) {
		return invalid()
	}
	beforeRoots := map[string]bool{}
	for _, name := range restoreManifestRoots(before) {
		beforeRoots[name] = true
	}
	j := restoreJournal{Version: 2, RestoreID: id, RootFingerprint: fingerprint, BeforeHome: *home, AfterHome: *home, Roots: []restoreRoot{}}
	evidence := restoreEvidence{beforeMarker: bytes.Clone(marker), afterMarker: bytes.Clone(marker)}
	for _, name := range roots {
		hadActive, present := active[name]
		if !present || !hadActive && beforeRoots[name] {
			return invalid()
		}
		j.Roots = append(j.Roots, restoreRoot{Name: name, HadActive: hadActive})
		if name == "documents" && after.DataHomeID != home.DataHomeID {
			j.AfterHome.DataHomeID = after.DataHomeID
			evidence.afterMarker, markerProblem = datahome.MarkerBytes(home.Channel, after.DataHomeID)
			if markerProblem != nil {
				return invalid()
			}
		}
	}
	evidence.beforeManifest, _ = json.Marshal(before)
	evidence.afterManifest, _ = json.Marshal(after)
	j.BeforeMarkerSHA256, j.AfterMarkerSHA256 = restoreDigest(evidence.beforeMarker), restoreDigest(evidence.afterMarker)
	j.BeforeManifestSHA256, j.AfterManifestSHA256 = restoreDigest(evidence.beforeManifest), restoreDigest(evidence.afterManifest)
	if !validRestoreJournal(j) {
		return invalid()
	}
	return j, evidence, nil
}

func restoreManifestRoots(manifest Manifest) []string {
	names := map[string]bool{}
	for _, file := range manifest.Files {
		names[strings.SplitN(file.RelativePath, "/", 2)[0]] = true
	}
	roots := make([]string, 0, len(names))
	for name := range names {
		roots = append(roots, name)
	}
	sort.Strings(roots)
	return roots
}

// Rebuild from the bound evidence so independent fields cannot contradict the
// selected roots, document identity adoption or original marker. No filesystem
// or key-store access, and no silent upgrade of Python journals.
func validateRestoreEvidence(j restoreJournal, e restoreEvidence) *Error {
	if !validRestoreJournal(j) || len(e.beforeMarker) > datahome.MaxMarkerBytes || len(e.afterMarker) > datahome.MaxMarkerBytes ||
		len(e.beforeManifest) > MaxManifestBytes || len(e.afterManifest) > MaxManifestBytes ||
		restoreDigest(e.beforeMarker) != j.BeforeMarkerSHA256 || restoreDigest(e.afterMarker) != j.AfterMarkerSHA256 ||
		restoreDigest(e.beforeManifest) != j.BeforeManifestSHA256 || restoreDigest(e.afterManifest) != j.AfterManifestSHA256 {
		return failure("BACKUP_RESTORE_EVIDENCE_INVALID")
	}
	var before, after Manifest
	if !restoreCanonicalJSON(e.beforeManifest, &before) || !restoreCanonicalJSON(e.afterManifest, &after) {
		return failure("BACKUP_RESTORE_EVIDENCE_INVALID")
	}
	active := map[string]bool{}
	for _, root := range j.Roots {
		active[root.Name] = root.HadActive
	}
	rebuilt, evidence, p := makeRestoreJournal(j.RestoreID, j.RootFingerprint, e.beforeMarker, before, after, active)
	if p != nil || !bytes.Equal(evidence.afterMarker, e.afterMarker) {
		return failure("BACKUP_RESTORE_EVIDENCE_INVALID")
	}
	want, p := encodeRestoreJournal(rebuilt, j.RootFingerprint)
	actual, q := encodeRestoreJournal(j, j.RootFingerprint)
	if p != nil || q != nil || !bytes.Equal(want, actual) {
		return failure("BACKUP_RESTORE_EVIDENCE_INVALID")
	}
	return nil
}
