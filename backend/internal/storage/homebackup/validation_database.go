package homebackup

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intake/resumelibrary"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

func validatePayloadDatabase(s payloadSource, dataRoot string, expected *int64) (version *int64, count int, problem *Error) {
	return validatePayloadDatabaseFrom(s, s, dataRoot, expected)
}

// Database bytes may come from an isolated normalized copy while Resume
// references must still resolve against the exact original staged payload.
func validatePayloadDatabaseFrom(s, database payloadSource, dataRoot string, expected *int64) (version *int64, count int, problem *Error) {
	// Native snapshots must be standalone. Legacy WAL-bearing archive admission
	// needs an explicit conversion/recovery workflow, not an implicit SQLite write.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, ok := database.files[databasePath+suffix]; ok {
			return nil, 0, failure("BACKUP_DATABASE_NOT_STANDALONE")
		}
	}
	file, present := database.files[databasePath]
	if !present {
		if expected != nil {
			return nil, 0, failure("BACKUP_SCHEMA_UNSUPPORTED")
		}
		return nil, 0, nil
	}
	lease, p := database.root.OpenBackupRead(s.ctx, database.prefix+databasePath, sqlitestore.MaxBackupBytes)
	if p != nil {
		return nil, 0, failure("BACKUP_DATABASE_VERIFY_FAILED")
	}
	defer func() {
		if lease.Close() != nil {
			version, count, problem = nil, 0, failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	if lease.Size() != file.SizeBytes {
		return nil, 0, failure("BACKUP_PAYLOAD_CHANGED")
	}
	var header [20]byte
	if _, err := lease.ReadAt(header[:], 0); err != nil || string(header[:16]) != "SQLite format 3\x00" || header[18] != 1 || header[19] != 1 {
		return nil, 0, failure("BACKUP_DATABASE_NOT_STANDALONE")
	}
	hash := sha256.New()
	if n, err := io.CopyBuffer(hash, io.NewSectionReader(lease, 0, lease.Size()), make([]byte, 64<<10)); err != nil || n != file.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return nil, 0, failure("BACKUP_PAYLOAD_CHANGED")
	}
	db, err := sqlitestore.OpenReadOnly(s.ctx, filepath.Join(dataRoot, filepath.FromSlash(database.prefix+databasePath)))
	if err != nil {
		return nil, 0, failure("BACKUP_DATABASE_VERIFY_FAILED")
	}
	defer func() {
		if db.Close() != nil {
			version, count, problem = nil, 0, failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	conn, err := db.Conn(s.ctx)
	if err != nil {
		return nil, 0, failure("BACKUP_DATABASE_VERIFY_FAILED")
	}
	defer func() {
		if conn.Close() != nil {
			version, count, problem = nil, 0, failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	var v int
	if err = conn.QueryRowContext(s.ctx, "PRAGMA user_version").Scan(&v); err != nil || v < 0 || v > applications.SchemaVersion || expected == nil || int64(v) != *expected {
		return nil, 0, failure("BACKUP_SCHEMA_UNSUPPORTED")
	}
	if err = sqlitestore.VerifyBackup(s.ctx, conn, v); err != nil {
		return nil, 0, failure("BACKUP_DATABASE_VERIFY_FAILED")
	}
	count, problem = validateResumeReferences(s, conn)
	if problem != nil {
		return nil, 0, problem
	}
	value := int64(v)
	return &value, count, nil
}

func validateResumeReferences(s payloadSource, conn *sql.Conn) (count int, problem *Error) {
	var exists int
	if err := conn.QueryRowContext(s.ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='resume_library'").Scan(&exists); err != nil {
		return 0, failure("BACKUP_RESUME_DATABASE_INVALID")
	}
	if exists == 0 {
		return 0, nil
	}
	columns, err := conn.QueryContext(s.ctx, "PRAGMA table_info(resume_library)")
	if err != nil {
		return 0, failure("BACKUP_RESUME_DATABASE_INVALID")
	}
	defer func() {
		if columns.Close() != nil {
			count, problem = 0, failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	seen := map[string]bool{}
	columnCount := 0
	for columns.Next() {
		columnCount++
		if columnCount > 256 {
			return 0, failure("BACKUP_PAYLOAD_LIMIT")
		}
		var cid, notNull, pk int
		var name, typeName string
		var defaultValue any
		if err := columns.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk); err != nil {
			return 0, failure("BACKUP_RESUME_DATABASE_INVALID")
		}
		seen[name] = true
	}
	if columns.Err() != nil {
		return 0, failure("BACKUP_RESUME_DATABASE_INVALID")
	}
	if err = columns.Close(); err != nil {
		return 0, failure("BACKUP_DATABASE_CLEANUP_FAILED")
	}
	if !seen["relative_path"] || !seen["sha256"] || !seen["size_bytes"] {
		return 0, nil
	}
	rows, err := conn.QueryContext(s.ctx, "SELECT relative_path, sha256, size_bytes FROM resume_library")
	if err != nil {
		return 0, failure("BACKUP_RESUME_DATABASE_INVALID")
	}
	defer func() {
		if rows.Close() != nil {
			count, problem = 0, failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	for rows.Next() {
		if count >= MaxFiles {
			return 0, failure("BACKUP_PAYLOAD_LIMIT")
		}
		var relativeValue, hashValue, sizeValue any
		if err := rows.Scan(&relativeValue, &hashValue, &sizeValue); err != nil {
			return 0, failure("BACKUP_RESUME_DATABASE_INVALID")
		}
		relative, ok := relativeValue.(string)
		digest, hashOK := hashValue.(string)
		size, sizeOK := sizeValue.(int64)
		if !ok || !hashOK || !sizeOK || !safeName(relative) || !strings.HasPrefix(relative, resumelibrary.LibraryPath+"/") || size < 0 || !lowerHex(digest, 64) {
			return 0, failure("BACKUP_RESUME_REFERENCE_INVALID")
		}
		file, present := s.files[relative]
		if !present || file.SHA256 != digest || file.SizeBytes != size {
			return 0, failure("BACKUP_RESUME_REFERENCE_INVALID")
		}
		lease, p := s.root.OpenBackupRead(s.ctx, s.prefix+relative, MaxEntryBytes)
		if p != nil {
			return 0, failure("BACKUP_RESUME_REFERENCE_INVALID")
		}
		hash := sha256.New()
		n, err := io.CopyBuffer(hash, io.NewSectionReader(lease, 0, lease.Size()), make([]byte, 64<<10))
		closeProblem := lease.Close()
		if err != nil || closeProblem != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
			return 0, failure("BACKUP_RESUME_REFERENCE_INVALID")
		}
		count++
	}
	if rows.Err() != nil {
		return 0, failure("BACKUP_RESUME_DATABASE_INVALID")
	}
	return count, nil
}
