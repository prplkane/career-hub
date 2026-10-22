package readonly

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/envelope"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/documents/ledger"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/secretstore"
)

const (
	documentRootPath   = "documents"
	documentFilesPath  = "documents/files"
	documentLedgerPath = "documents/ledger.chdoc"
	documentLegacyPath = "documents/ledger.dpapi"
	documentStatePath  = "documents/key_state.json"
)

type documentRead struct {
	records   []ledger.Document
	encrypted []byte
}
type documentBackendFactory func() (keys.Backend, error)

func nativeDocumentBackend() (keys.Backend, error) { return secretstore.NewWindowsBackend() }

// Documents is the metadata view, never a model-context projection. It does not
// read/decrypt blob contents, generate keys, migrate, create files or cache data.
// includeArchived=true preserves the source list_documents default.
func (w *Workspace) Documents(ctx context.Context, includeArchived bool) (*ledger.Listing, error) {
	read, e := w.readDocuments(ctx, nativeDocumentBackend)
	if e != nil {
		return nil, e
	}
	result := ledger.Metadata(read.records, read.encrypted, includeArchived)
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	return &result, nil
}

// DocumentTitles is the only document projection permitted in model context.
// Authentication and full file membership are still required for this view.
func (w *Workspace) DocumentTitles(ctx context.Context) ([]string, error) {
	read, e := w.readDocuments(ctx, nativeDocumentBackend)
	if e != nil {
		return nil, e
	}
	titles := ledger.Titles(read.records)
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	return titles, nil
}

func documentCanceled(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return fail("DOCUMENT_READ_CANCELED")
	}
	return nil
}

func (w *Workspace) readDocuments(ctx context.Context, backend documentBackendFactory) (*documentRead, error) {
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	if w == nil || w.dataHomeID == "" {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if _, e := validateRoots(w.start); e != nil {
		return nil, e
	}
	return admittedDocuments(ctx, w.dataHomeID,
		func() (*datahome.Info, *datahome.Error) { return datahome.Inspect(w.start.DataRoot, &w.start.Channel) },
		func(info datahome.Info) (*documentRead, error) {
			return readOwnedDocuments(ctx, w.start.DataRoot, info, backend)
		})
}

// Admission after load runs only after all synchronous file/credential work and
// cleanup have returned. No timed-out native operation is abandoned in a goroutine.
func admittedDocuments(ctx context.Context, expectedID string,
	inspect func() (*datahome.Info, *datahome.Error),
	load func(datahome.Info) (*documentRead, error),
) (*documentRead, error) {
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	if expectedID == "" {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	before, issue := inspect()
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	if issue != nil {
		return nil, admissionError(issue)
	}
	if before == nil || !before.Initialized {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if before.DataHomeID != expectedID {
		return nil, fail("DATA_HOME_IDENTITY_CHANGED")
	}
	identity, channel, format := before.DataHomeID, before.Channel, before.FormatVersion
	result, loadError := load(*before)
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	after, issue := inspect()
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	if issue != nil {
		return nil, admissionError(issue)
	}
	if after == nil || !after.Initialized {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if identity != after.DataHomeID || channel != after.Channel || format != after.FormatVersion {
		return nil, fail("DATA_HOME_IDENTITY_CHANGED")
	}
	if loadError != nil {
		return nil, loadError
	}
	if result == nil || result.records == nil {
		return nil, fail("DOCUMENT_LEDGER_INVALID")
	}
	return result, nil
}

func readOwnedDocuments(ctx context.Context, dataRoot string, info datahome.Info, backend documentBackendFactory) (result *documentRead, problem error) {
	root, e := runtimepaths.Open(dataRoot)
	if e != nil {
		return nil, fail("DOCUMENT_STORAGE_INVALID")
	}
	defer func() {
		if root.Close() != nil {
			result, problem = nil, fail("DOCUMENT_STORAGE_INVALID")
		}
	}()
	if e := root.RequireProcessOwnership(); e != nil {
		switch e.Code {
		case "OWNERSHIP_REQUIRED":
			return nil, fail("DATA_HOME_OWNERSHIP_REQUIRED")
		case "OWNERSHIP_UNSUPPORTED":
			return nil, fail("DOCUMENT_STORAGE_UNSUPPORTED")
		default:
			return nil, fail("DOCUMENT_STORAGE_INVALID")
		}
	}
	s := documentStorage{ctx: ctx, root: root, dataRoot: dataRoot}
	if e := s.checkRoot(); e != nil {
		return nil, e
	}
	manager, e2 := s.loadContext(info, backend)
	if e2 != nil {
		return nil, e2
	}
	found, e2 := s.exists(documentLedgerPath)
	if e2 != nil {
		return nil, e2
	}
	if !found {
		names, e := s.fileNames()
		if e != nil {
			return nil, e
		}
		if len(names) != 0 {
			return nil, fail("DOCUMENT_LEDGER_INVALID")
		}
		return &documentRead{records: []ledger.Document{}}, nil
	}
	if manager == nil {
		return nil, fail("DOCUMENT_KEY_STATE_MISSING")
	}
	// Keep the format limit independent of the managed larger-object ceiling.
	encrypted, present, pathError := root.ReadObjectFile(documentLedgerPath, envelope.MaxEnvelopeBytes)
	if pathError != nil {
		if pathError.Code == "FILE_SIZE_LIMIT" {
			return nil, fail("DOCUMENT_ENCRYPTION_INVALID")
		}
		return nil, documentFileReadError(pathError, "DOCUMENT_LEDGER_INVALID")
	}
	if !present {
		return nil, fail("DOCUMENT_LEDGER_INVALID")
	}
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	header, err := envelope.Inspect(encrypted)
	if err != nil {
		return nil, documentCryptoError(err)
	}
	// Only the retained key selector comes from the header. Home, purpose and
	// object identity remain independent, already admitted caller expectations.
	key, err := manager.LoadKey(info.DataHomeID, header.KeyID)
	if err != nil {
		return nil, documentCryptoError(err)
	}
	defer clear(key) // Owned copy only; not a guarantee about runtime copies.
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	opened, err := envelope.Decrypt(encrypted, key, envelope.Context{DataHomeID: info.DataHomeID, KeyID: header.KeyID, Purpose: "ledger", ObjectID: "ledger"})
	if err != nil {
		return nil, documentCryptoError(err)
	}
	defer clear(opened.Plaintext)
	documents, parseError := ledger.Parse(opened.Plaintext)
	if parseError != nil {
		if parseError.Code == ledger.CodeUnsupported {
			return nil, fail(ledger.CodeUnsupported)
		}
		return nil, fail("DOCUMENT_LEDGER_INVALID")
	}
	names, e2 := s.fileNames()
	if e2 != nil {
		return nil, e2
	}
	if len(names) != len(documents) {
		return nil, fail("DOCUMENT_LEDGER_INVALID")
	}
	expected := make(map[string]bool, len(documents))
	for _, document := range documents {
		expected[document.ID+".chdoc"] = true
	}
	for _, name := range names {
		if !expected[name] {
			return nil, fail("DOCUMENT_LEDGER_INVALID")
		}
	}
	if e := documentCanceled(ctx); e != nil {
		return nil, e
	}
	return &documentRead{records: documents, encrypted: encrypted}, nil
}

type documentStorage struct {
	ctx      context.Context
	root     *runtimepaths.Root
	dataRoot string
}

func (s documentStorage) exists(path string) (bool, error) {
	if e := documentCanceled(s.ctx); e != nil {
		return false, e
	}
	found, e := s.root.Exists(path)
	if e != nil {
		return false, fail("DOCUMENT_STORAGE_INVALID")
	}
	return found, nil
}

func (s documentStorage) checkRoot() error {
	found, e := s.exists(documentRootPath)
	if e != nil || !found {
		return e
	}
	directory, pathError := runtimepaths.Open(filepath.Join(s.dataRoot, documentRootPath))
	if pathError != nil {
		return fail("DOCUMENT_STORAGE_INVALID")
	}
	if directory.Close() != nil {
		return fail("DOCUMENT_STORAGE_INVALID")
	}
	return nil
}

func (s documentStorage) fileNames() (names []string, problem error) {
	found, e := s.exists(documentFilesPath)
	if e != nil {
		return nil, e
	}
	if !found {
		return []string{}, nil
	}
	directory, pathError := runtimepaths.Open(filepath.Join(s.dataRoot, filepath.FromSlash(documentFilesPath)))
	if pathError != nil {
		if pathError.Code == "ROOT_NOT_DIRECTORY" {
			return nil, fail("DOCUMENT_FILE_INVALID")
		}
		return nil, fail("DOCUMENT_STORAGE_INVALID")
	}
	defer func() {
		if directory.Close() != nil {
			names, problem = nil, fail("DOCUMENT_STORAGE_INVALID")
		}
	}()
	names, pathError = directory.ListFileNames()
	if pathError != nil {
		switch pathError.Code {
		case "PATH_NOT_REGULAR":
			return nil, fail("DOCUMENT_FILE_INVALID")
		case "PATH_DIRECTORY_LIMIT":
			return nil, fail("DOCUMENT_STORAGE_LIMIT")
		default:
			return nil, fail("DOCUMENT_STORAGE_INVALID")
		}
	}
	if e := documentCanceled(s.ctx); e != nil {
		return nil, e
	}
	return names, nil
}

func (s documentStorage) loadContext(info datahome.Info, factory documentBackendFactory) (*keys.Manager, error) {
	legacy, e := s.exists(documentLegacyPath)
	if e != nil {
		return nil, e
	}
	if legacy {
		return nil, fail("DOCUMENT_MIGRATION_REQUIRED")
	}
	names, e := s.fileNames()
	if e != nil {
		return nil, e
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".dpapi") {
			return nil, fail("DOCUMENT_MIGRATION_REQUIRED")
		}
	}
	found, e := s.exists(documentStatePath)
	if e != nil {
		return nil, e
	}
	if !found {
		ciphertext, e := s.exists(documentLedgerPath)
		if e != nil {
			return nil, e
		}
		if ciphertext {
			return nil, fail("DOCUMENT_KEY_STATE_MISSING")
		}
		names, e := s.fileNames()
		if e != nil {
			return nil, e
		}
		if len(names) > 0 {
			return nil, fail("DOCUMENT_KEY_STATE_MISSING")
		}
		return nil, nil
	}
	raw, present, pathError := s.root.ReadFile(documentStatePath, keys.MaxStateBytes)
	if pathError != nil {
		if pathError.Code == "FILE_SIZE_LIMIT" {
			return nil, fail("DOCUMENT_ENCRYPTION_INVALID")
		}
		return nil, documentFileReadError(pathError, "DOCUMENT_ENCRYPTION_FAILED")
	}
	if !present {
		return nil, fail("DOCUMENT_ENCRYPTION_FAILED")
	}
	state, err := keys.ParseState(raw)
	if err != nil {
		return nil, documentCryptoError(err)
	}
	if state.DataHomeID != info.DataHomeID {
		return nil, fail("DOCUMENT_ENCRYPTION_INVALID")
	}
	if e := documentCanceled(s.ctx); e != nil {
		return nil, e
	}
	if factory == nil {
		return nil, fail("DOCUMENT_ENCRYPTION_UNAVAILABLE")
	}
	backend, err := factory()
	if err != nil {
		return nil, documentCryptoError(err)
	}
	manager, err := keys.NewManager(info.Channel, backend, nil)
	if err != nil {
		return nil, documentCryptoError(err)
	}
	// Availability of the active key is required even when a retained key will
	// decrypt the ledger, or the ledger is absent. Never cache/regenerate it.
	master, err := manager.Load(state)
	if err != nil {
		return nil, documentCryptoError(err)
	}
	clear(master.Key) // Availability check is complete; retain no active-key cache.
	if e := documentCanceled(s.ctx); e != nil {
		return nil, e
	}
	return manager, nil
}

func documentFileReadError(e *runtimepaths.Error, fallback string) error {
	switch e.Code {
	case "FILE_OPEN", "FILE_READ", "FILE_STAT", "FILE_CLOSE", "ROOT_CLOSE", "PATH_NOT_REGULAR":
		return fail(fallback)
	default:
		return fail("DOCUMENT_STORAGE_INVALID")
	}
}

// No native cause/target/key survives this service boundary, even via Unwrap.
func documentCryptoError(err error) error {
	var keyError *keys.Error
	if errors.As(err, &keyError) {
		if keyError.Code == keys.CodeUnavailable {
			return fail("DOCUMENT_KEY_UNAVAILABLE")
		}
		if keyError.Code == keys.CodeBackendRequired {
			return fail("DOCUMENT_ENCRYPTION_UNAVAILABLE")
		}
		return fail("DOCUMENT_ENCRYPTION_INVALID")
	}
	var encryptionError *envelope.Error
	if errors.As(err, &encryptionError) {
		return fail("DOCUMENT_ENCRYPTION_INVALID")
	}
	var backendError *secretstore.BackendError
	if errors.As(err, &backendError) {
		return fail("DOCUMENT_ENCRYPTION_UNAVAILABLE")
	}
	return fail("DOCUMENT_ENCRYPTION_FAILED")
}
