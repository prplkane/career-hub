package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sidecar/httpapi"
)

// The extension pairing record (ADR SEC-250 item 4) lives next to the agent
// connection file in the Data Home's session/ directory: protected owner+SYSTEM
// DACL and outside the backup allowlist. Unlike the agent file it survives
// shutdown, because the owner pairs once. It holds only the credential's
// SHA-256, so reading it does not yield a usable credential.
const extensionPairingPath = "session/extension-pairing.json"

var errExtensionPairingStore = errors.New("extension pairing record unavailable")

type extensionPairingFile struct {
	CredentialSHA256 string `json:"credential_sha256"`
	PairedAt         string `json:"paired_at"`
}

type dataHomePairingStore struct{ dataRoot string }

func (s dataHomePairingStore) Load() (httpapi.PairingRecord, bool, error) {
	root, problem := runtimepaths.Open(s.dataRoot)
	if problem != nil {
		return httpapi.PairingRecord{}, false, errExtensionPairingStore
	}
	defer root.Close()
	raw, found, problem := root.ReadSingleLinkFile(extensionPairingPath, agentConnectionMaxBytes)
	if problem != nil {
		return httpapi.PairingRecord{}, false, errExtensionPairingStore
	}
	if !found {
		return httpapi.PairingRecord{}, false, nil
	}
	var file extensionPairingFile
	if json.Unmarshal(raw, &file) != nil {
		return httpapi.PairingRecord{}, false, errExtensionPairingStore
	}
	decoded, err := hex.DecodeString(file.CredentialSHA256)
	pairedAt, timeErr := time.Parse(time.RFC3339, file.PairedAt)
	if err != nil || len(decoded) != 32 || timeErr != nil {
		return httpapi.PairingRecord{}, false, errExtensionPairingStore
	}
	var record httpapi.PairingRecord
	copy(record.CredentialSHA256[:], decoded)
	record.PairedAt = pairedAt
	return record, true, nil
}

func (s dataHomePairingStore) Save(record httpapi.PairingRecord) error {
	content, err := json.Marshal(extensionPairingFile{
		CredentialSHA256: hex.EncodeToString(record.CredentialSHA256[:]),
		PairedAt:         record.PairedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return errExtensionPairingStore
	}
	root, problem := runtimepaths.Open(s.dataRoot)
	if problem != nil {
		return errExtensionPairingStore
	}
	defer root.Close()
	if _, problem := root.EnsureDirectory(agentConnectionDirectory); problem != nil {
		return errExtensionPairingStore
	}
	// Re-pairing replaces the previous record: one paired extension at a time.
	if err := removeExactFile(root, extensionPairingPath); err != nil {
		return errExtensionPairingStore
	}
	if created, problem := root.CreateFile(extensionPairingPath, content); problem != nil || !created {
		return errExtensionPairingStore
	}
	return nil
}

func (s dataHomePairingStore) Delete() error {
	root, problem := runtimepaths.Open(s.dataRoot)
	if problem != nil {
		return errExtensionPairingStore
	}
	defer root.Close()
	return removeExactFile(root, extensionPairingPath)
}
