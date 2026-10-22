package homebackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vaultread"
	"golang.org/x/text/unicode/norm"
)

// PayloadValidation contains counts only, never document/profile values. It is
// not a backup publication, restore authorization or key portability promise.
type PayloadValidation struct {
	VaultValidated          bool   `json:"vault_validated"`
	NormalizedJobsValidated int    `json:"normalized_jobs_validated"`
	SQLiteSchemaVersion     *int64 `json:"sqlite_schema_version"`
	ResumeFilesValidated    int    `json:"resume_files_validated"`
	DocumentsValidated      int    `json:"documents_validated"`
}

type payloadSource struct {
	ctx    context.Context
	root   *runtimepaths.Root
	prefix string
	files  map[string]File
}

// ValidateSnapshot validates manifest-bound staged contents without migration,
// key creation or backend discovery. The caller owns the staging reservation,
// maintenance admission and writer quiescence. Only a supplied existing-key
// backend can authenticate encrypted documents; no credentials are exported.
func ValidateSnapshot(ctx context.Context, dataRoot, snapshot string, input Manifest, backend keys.Backend) (result *PayloadValidation, problem *Error) {
	return validateSnapshot(ctx, dataRoot, snapshot, input, backend, validatePayloadDatabase)
}

func validateSnapshot(ctx context.Context, dataRoot, snapshot string, input Manifest, backend keys.Backend, database func(payloadSource, string, *int64) (*int64, int, *Error)) (result *PayloadValidation, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("BACKUP_CANCELED")
	}
	backupStage := strings.HasPrefix(snapshot, snapshotPrefix) && lowerHex(strings.TrimPrefix(snapshot, snapshotPrefix), 32)
	restoreStage := strings.HasPrefix(snapshot, restorePrefix) && lowerHex(strings.TrimPrefix(snapshot, restorePrefix), 32)
	if !backupStage && !restoreStage {
		return nil, failure("BACKUP_SNAPSHOT_INVALID")
	}
	manifest, p := validationManifest(input)
	if p != nil {
		return nil, p
	}
	root, pathProblem := runtimepaths.Open(dataRoot)
	if pathProblem != nil {
		return nil, failure("BACKUP_SNAPSHOT_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			result, problem = nil, failure("BACKUP_SNAPSHOT_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return nil, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	source := payloadSource{ctx: ctx, root: root, prefix: snapshot + "/", files: map[string]File{}}
	for _, file := range manifest.Files {
		source.files[file.RelativePath] = file
	}
	if p = validatePayloadPaths(manifest.Files); p != nil {
		return nil, p
	}
	result = &PayloadValidation{}
	if result.VaultValidated, p = validatePayloadVault(source); p != nil {
		return nil, p
	}
	if result.NormalizedJobsValidated, p = validatePayloadJobs(source, manifest.Files); p != nil {
		return nil, p
	}
	if result.SQLiteSchemaVersion, result.ResumeFilesValidated, p = database(source, dataRoot, manifest.SQLiteSchemaVersion); p != nil {
		return nil, p
	}
	if result.DocumentsValidated, p = validatePayloadDocuments(source, manifest, backend); p != nil {
		return nil, p
	}
	if ctx.Err() != nil {
		return nil, failure("BACKUP_CANCELED")
	}
	return result, nil
}

func validatePayloadPaths(files []File) *Error {
	// Semantic readers use canonical paths while Windows resolves case aliases.
	// Refuse aliases before extraction/credential lookup rather than allowing a
	// payload to look absent to validation and become active after restoration.
	reservedFiles := map[string]string{}
	reservedDirectories := map[string]string{}
	reserveDirectories := func(name string) {
		parts := strings.Split(name, "/")
		for n := 1; n <= len(parts); n++ {
			prefix := strings.Join(parts[:n], "/")
			reservedDirectories[textcompat.Fold(prefix)] = prefix
		}
	}
	canonicalFiles := append(vaultread.SetupPaths(), databasePath, databasePath+"-wal", databasePath+"-shm", databasePath+"-journal",
		"documents/key_state.json", "documents/ledger.chdoc", "documents/ledger.dpapi")
	for _, name := range canonicalFiles {
		reservedFiles[textcompat.Fold(name)] = name
		reserveDirectories(name[:strings.LastIndex(name, "/")])
	}
	for _, directory := range []string{"jobs/normalized", "documents/files", resumelibrary.LibraryPath} {
		reserveDirectories(directory)
	}
	names := make(map[string]bool, len(files))
	for _, file := range files {
		parts := strings.Split(file.RelativePath, "/")
		for n := 1; n <= len(parts); n++ {
			prefix := strings.Join(parts[:n], "/")
			folded := textcompat.Fold(norm.NFC.String(prefix))
			if canonical, reserved := reservedFiles[folded]; reserved && (prefix != canonical || n != len(parts)) {
				return failure("BACKUP_PAYLOAD_PATH_INVALID")
			}
			if canonical, reserved := reservedDirectories[folded]; reserved && (prefix != canonical || n == len(parts)) {
				return failure("BACKUP_PAYLOAD_PATH_INVALID")
			}
		}
		key := textcompat.Fold(norm.NFC.String(file.RelativePath))
		if names[key] {
			return failure("BACKUP_PAYLOAD_PATH_INVALID")
		}
		names[key] = true
	}
	for name := range names {
		parts := strings.Split(name, "/")
		if len(parts) < 2 {
			return failure("BACKUP_PAYLOAD_PATH_INVALID")
		}
		for n := 1; n < len(parts); n++ {
			if names[strings.Join(parts[:n], "/")] {
				return failure("BACKUP_PAYLOAD_PATH_INVALID")
			}
		}
	}
	return nil
}

func validationManifest(input Manifest) (Manifest, *Error) {
	if len(input.Files) > MaxFiles {
		return Manifest{}, failure("BACKUP_MANIFEST_INVALID")
	}
	remaining := MaxManifestBytes
	check := func(value string) bool {
		if len(value) > remaining {
			return false
		}
		remaining -= len(value)
		return true
	}
	if !check(input.DataHomeID) || !check(input.DataHomeChannel) {
		return Manifest{}, failure("BACKUP_MANIFEST_INVALID")
	}
	for _, file := range input.Files {
		if !check(file.RelativePath) || !check(file.SHA256) {
			return Manifest{}, failure("BACKUP_MANIFEST_INVALID")
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return Manifest{}, failure("BACKUP_MANIFEST_INVALID")
	}
	return ParseManifest(raw)
}

func (s payloadSource) read(relative string, limit int64) ([]byte, bool, *Error) {
	if s.ctx.Err() != nil {
		return nil, false, failure("BACKUP_CANCELED")
	}
	file, present := s.files[relative]
	if !present {
		return nil, false, nil
	}
	if file.SizeBytes > limit {
		return nil, false, failure("BACKUP_PAYLOAD_LIMIT")
	}
	raw, found, p := s.root.ReadObjectFile(s.prefix+relative, limit)
	if s.ctx.Err() != nil {
		clear(raw)
		return nil, false, failure("BACKUP_CANCELED")
	}
	if p != nil || !found || int64(len(raw)) != file.SizeBytes {
		clear(raw)
		return nil, false, failure("BACKUP_PAYLOAD_CHANGED")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != file.SHA256 {
		clear(raw)
		return nil, false, failure("BACKUP_PAYLOAD_CHANGED")
	}
	return raw, true, nil
}

func validatePayloadVault(s payloadSource) (bool, *Error) {
	files := map[string][]byte{}
	defer func() {
		for _, raw := range files {
			clear(raw)
		}
	}()
	paths := vaultread.SetupPaths()
	for _, name := range paths {
		raw, present, p := s.read(name, cataloginput.YAMLMaxInputBytes)
		if p != nil {
			return false, p
		}
		if present {
			files[name] = raw
		}
	}
	if len(files) == 0 {
		return false, nil
	}
	if len(files) != len(paths) {
		return false, failure("BACKUP_VAULT_INCOMPLETE")
	}
	if p := vaultread.ValidateSetupFiles(s.ctx, files); p != nil {
		return false, failure("BACKUP_VAULT_INVALID")
	}
	return true, nil
}

func validatePayloadJobs(s payloadSource, files []File) (int, *Error) {
	count := 0
	seen := map[string]bool{}
	for _, file := range files {
		if !strings.HasPrefix(file.RelativePath, "jobs/normalized/") {
			continue
		}
		name := strings.TrimPrefix(file.RelativePath, "jobs/normalized/")
		// Windows Path.glob is case-insensitive; .yml is additionally checked so
		// a supported native Job Card cannot evade backup validation by extension.
		if strings.Contains(name, "/") || !(strings.HasSuffix(strings.ToLower(name), ".yaml") || strings.HasSuffix(strings.ToLower(name), ".yml")) {
			continue
		}
		raw, _, p := s.read(file.RelativePath, cataloginput.YAMLMaxInputBytes)
		if p != nil {
			return 0, p
		}
		value, parseError := cataloginput.ParseYAML(raw)
		clear(raw)
		if parseError != nil {
			return 0, failure("BACKUP_JOB_INVALID")
		}
		report, problem := jobcard.Validate(s.ctx, value, func(ctx context.Context, path string) (bool, *jobcard.Error) {
			if ctx.Err() != nil {
				return false, &jobcard.Error{Code: "JOB_CARD_CANCELED"}
			}
			file, present := s.files[path]
			if !present {
				return false, nil
			}
			lease, p := s.root.OpenBackupRead(ctx, s.prefix+path, MaxEntryBytes)
			if p != nil {
				return false, &jobcard.Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
			}
			valid := lease.Size() == file.SizeBytes
			if p = lease.Close(); p != nil || !valid {
				return false, &jobcard.Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
			}
			return true, nil
		})
		if problem != nil || !report.SchemaValid() {
			return 0, failure("BACKUP_JOB_INVALID")
		}
		for _, entry := range value.Entries {
			if entry.Key.Kind == "string" && entry.Key.Text == "id" {
				id := entry.Value.Text
				if seen[id] {
					return 0, failure("BACKUP_JOB_DUPLICATE")
				}
				seen[id] = true
			}
		}
		count++
	}
	return count, nil
}
