package homebackup

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"careerhub.local/backend/internal/storage/documents/envelope"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/documents/ledger"
)

func validatePayloadDocuments(s payloadSource, manifest Manifest, backend keys.Backend) (int, *Error) {
	const statePath = "documents/key_state.json"
	const ledgerPath = "documents/ledger.chdoc"
	if _, legacy := s.files["documents/ledger.dpapi"]; legacy {
		return 0, failure("BACKUP_DOCUMENT_MIGRATION_REQUIRED")
	}
	names := map[string]bool{}
	for name := range s.files {
		if strings.HasPrefix(name, "documents/files/") {
			leaf := strings.TrimPrefix(name, "documents/files/")
			if strings.HasSuffix(leaf, ".dpapi") {
				return 0, failure("BACKUP_DOCUMENT_MIGRATION_REQUIRED")
			}
			if strings.Contains(leaf, "/") {
				return 0, failure("BACKUP_DOCUMENT_STORAGE_INVALID")
			}
			names[leaf] = true
		}
	}
	raw, statePresent, p := s.read(statePath, keys.MaxStateBytes)
	if p != nil {
		return 0, p
	}
	_, ledgerPresent := s.files[ledgerPath]
	if !statePresent {
		if ledgerPresent || len(names) > 0 {
			return 0, failure("BACKUP_DOCUMENT_KEY_STATE_MISSING")
		}
		return 0, nil
	}
	state, err := keys.ParseState(raw)
	clear(raw)
	if err != nil || state.DataHomeID != manifest.DataHomeID {
		return 0, failure("BACKUP_DOCUMENT_IDENTITY_INVALID")
	}
	manager, err := keys.NewManager(manifest.DataHomeChannel, backend, nil)
	if err != nil {
		return 0, failure("BACKUP_DOCUMENT_KEY_UNAVAILABLE")
	}
	// Active-key availability is required even for an empty ledger or one sealed
	// under a retained older key. Only Load/LoadKey are used; never Create/Delete.
	master, err := manager.Load(state)
	clear(master.Key)
	if err != nil {
		return 0, failure("BACKUP_DOCUMENT_KEY_UNAVAILABLE")
	}
	if s.ctx.Err() != nil {
		return 0, failure("BACKUP_CANCELED")
	}
	if !ledgerPresent {
		if len(names) > 0 {
			return 0, failure("BACKUP_DOCUMENT_STORAGE_INVALID")
		}
		return 0, nil
	}
	plaintext, p := openPayloadDocument(s, manager, manifest.DataHomeID, ledgerPath, "ledger", "ledger")
	if p != nil {
		return 0, p
	}
	records, parseProblem := ledger.Parse(plaintext)
	clear(plaintext)
	if parseProblem != nil || len(names) != len(records) {
		return 0, failure("BACKUP_DOCUMENT_STORAGE_INVALID")
	}
	for _, record := range records {
		name := record.ID + ".chdoc"
		if !names[name] {
			return 0, failure("BACKUP_DOCUMENT_STORAGE_INVALID")
		}
		plaintext, p := openPayloadDocument(s, manager, manifest.DataHomeID, "documents/files/"+name, "blob", record.ID)
		if p != nil {
			return 0, p
		}
		digest := sha256.Sum256(plaintext)
		valid := len(plaintext) == record.SizeBytes && hex.EncodeToString(digest[:]) == record.SHA256
		clear(plaintext)
		if !valid {
			return 0, failure("BACKUP_DOCUMENT_CONTENT_INVALID")
		}
	}
	return len(records), nil
}

func openPayloadDocument(s payloadSource, manager *keys.Manager, homeID, relative, purpose, objectID string) ([]byte, *Error) {
	ciphertext, present, p := s.read(relative, envelope.MaxEnvelopeBytes)
	if p != nil {
		return nil, p
	}
	if !present {
		return nil, failure("BACKUP_DOCUMENT_STORAGE_INVALID")
	}
	defer clear(ciphertext)
	header, err := envelope.Inspect(ciphertext)
	if err != nil || header.DataHomeID != homeID || header.Purpose != purpose || header.ObjectID != objectID {
		return nil, failure("BACKUP_DOCUMENT_IDENTITY_INVALID")
	}
	key, err := manager.LoadKey(homeID, header.KeyID)
	if err != nil {
		return nil, failure("BACKUP_DOCUMENT_KEY_UNAVAILABLE")
	}
	defer clear(key)
	if s.ctx.Err() != nil {
		return nil, failure("BACKUP_CANCELED")
	}
	opened, err := envelope.Decrypt(ciphertext, key, envelope.Context{DataHomeID: homeID, KeyID: header.KeyID, Purpose: purpose, ObjectID: objectID})
	if err != nil {
		return nil, failure("BACKUP_DOCUMENT_AUTHENTICATION_FAILED")
	}
	if s.ctx.Err() != nil {
		clear(opened.Plaintext)
		return nil, failure("BACKUP_CANCELED")
	}
	return opened.Plaintext, nil
}
