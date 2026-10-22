package documentmigration

import (
	"bytes"
	"fmt"
	"io"
	"sort"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/ledger"
	"careerhub.local/backend/internal/documents/legacydpapi"
)

const (
	CodeDataHomeInvalid = "DOCUMENT_MIGRATION_DATA_HOME_INVALID"
	CodeNotRequired     = "DOCUMENT_MIGRATION_NOT_REQUIRED"
	CodeRequiresWindows = "DOCUMENT_MIGRATION_REQUIRES_WINDOWS"
	CodeSourceInvalid   = "DOCUMENT_MIGRATION_SOURCE_INVALID"
)

// Error retains only a stable source-compatible category. It never wraps or
// formats marker bytes, ciphertext, plaintext, document IDs, paths, or native
// errors.
type Error struct{ Code string }

func (e *Error) Error() string              { return "document migration validation failed" }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func fail(code string) *Error               { return &Error{Code: code} }

// sourceBytes is the result of a future confined, bounded tree read. The
// filesystem adapter must prove directory shape, ordinary-file identity,
// exact-name membership, process ownership, and recovery-sentinel admission.
// This value alone grants no path or write authority.
type sourceBytes struct {
	Marker               []byte
	DocumentsRootPresent bool
	LedgerPresent        bool
	LedgerCiphertext     []byte
	Files                map[string][]byte
	Windows              bool
}

// unprotectFunc returns a fresh caller-owned plaintext buffer. The caller must
// clear every non-nil result, including a partial result returned with an error.
type unprotectFunc func([]byte) ([]byte, error)
type ciphertextReader func(string) ([]byte, *Error)

type legacySourceDocument struct {
	ID         string
	Ciphertext []byte
	Plaintext  []byte
}

// legacySourceSnapshot owns the sensitive buffers returned by the validated
// source pass. Callers must defer clear immediately after receipt.
type legacySourceSnapshot struct {
	Marker               []byte
	Channel              string
	DocumentsRootPresent bool
	LedgerCiphertext     []byte
	LedgerPlaintext      []byte
	Documents            []legacySourceDocument
}

func (snapshot *legacySourceSnapshot) clear() {
	if snapshot == nil {
		return
	}
	clear(snapshot.Marker)
	clear(snapshot.LedgerCiphertext)
	clear(snapshot.LedgerPlaintext)
	for index := range snapshot.Documents {
		clear(snapshot.Documents[index].Ciphertext)
		clear(snapshot.Documents[index].Plaintext)
	}
	*snapshot = legacySourceSnapshot{}
}

// validateSourceBytes performs the source's marker, DPAPI, ledger, membership,
// and plaintext-integrity checks over already bounded owned bytes. It does not
// access the filesystem, native secret store, or default DPAPI implementation.
func validateSourceBytes(source sourceBytes, unprotect unprotectFunc) (Plan, *Error) {
	names := make([]string, 0, len(source.Files))
	for name := range source.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return validateSource(source.Marker, source.DocumentsRootPresent,
		source.LedgerPresent, source.LedgerCiphertext, names, source.Windows,
		func(name string) ([]byte, *Error) {
			content, present := source.Files[name]
			if !present {
				return nil, fail(CodeSourceInvalid)
			}
			return content, nil
		}, unprotect)
}

func validateSource(marker []byte, documentsRootPresent, ledgerPresent bool,
	ledgerCiphertext []byte, fileNames []string, windows bool,
	readCiphertext ciphertextReader, unprotect unprotectFunc) (Plan, *Error) {
	plan, snapshot, problem := validateSourceSnapshot(marker, documentsRootPresent, ledgerPresent,
		ledgerCiphertext, fileNames, windows, readCiphertext, unprotect)
	snapshot.clear()
	return plan, problem
}

func validateSourceSnapshot(marker []byte, documentsRootPresent, ledgerPresent bool,
	ledgerCiphertext []byte, fileNames []string, windows bool,
	readCiphertext ciphertextReader, unprotect unprotectFunc) (plan Plan, snapshot legacySourceSnapshot, problem *Error) {
	valid := false
	defer func() {
		if !valid {
			snapshot.clear()
		}
	}()
	info, markerError := datahome.ParseMarkerForDocumentMigration(marker)
	if markerError != nil {
		return Plan{}, snapshot, fail(CodeDataHomeInvalid)
	}
	if info.FormatVersion != SourceFormatVersion {
		return Plan{}, snapshot, fail(CodeNotRequired)
	}
	snapshot = legacySourceSnapshot{
		Marker: bytes.Clone(marker), Channel: info.Channel,
		DocumentsRootPresent: documentsRootPresent,
	}
	if !documentsRootPresent {
		if ledgerPresent || len(fileNames) != 0 {
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
		valid = true
		return planForValidatedSnapshot(validatedSnapshot{MarkerSHA256: digest(marker)}), snapshot, nil
	}
	if !ledgerPresent {
		if len(fileNames) != 0 {
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
		valid = true
		return planForValidatedSnapshot(validatedSnapshot{
			MarkerSHA256: digest(marker), DocumentsRootPresent: true,
		}), snapshot, nil
	}
	if !windows {
		return Plan{}, snapshot, fail(CodeRequiresWindows)
	}
	if unprotect == nil || readCiphertext == nil || !validCiphertext(ledgerCiphertext) {
		return Plan{}, snapshot, fail(CodeSourceInvalid)
	}
	ledgerPlaintext, err := unprotect(ledgerCiphertext)
	if err != nil {
		clear(ledgerPlaintext)
		return Plan{}, snapshot, fail(CodeSourceInvalid)
	}
	snapshot.LedgerCiphertext = bytes.Clone(ledgerCiphertext)
	snapshot.LedgerPlaintext = ledgerPlaintext
	records, parseError := ledger.Parse(ledgerPlaintext)
	if parseError != nil {
		return Plan{}, snapshot, fail(CodeSourceInvalid)
	}
	if len(fileNames) != len(records) {
		return Plan{}, snapshot, fail(CodeSourceInvalid)
	}
	available := make(map[string]bool, len(fileNames))
	for _, name := range fileNames {
		if available[name] {
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
		available[name] = true
	}
	documents := make([]validatedDocument, 0, len(records))
	for _, record := range records {
		name := record.ID + ".dpapi"
		if !available[name] {
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
		ciphertext, readError := readCiphertext(name)
		if readError != nil {
			return Plan{}, snapshot, readError
		}
		if !validCiphertext(ciphertext) {
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
		plaintext, err := unprotect(ciphertext)
		if err != nil {
			clear(plaintext)
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
		snapshot.Documents = append(snapshot.Documents, legacySourceDocument{
			ID: record.ID, Ciphertext: bytes.Clone(ciphertext), Plaintext: plaintext,
		})
		if len(plaintext) != record.SizeBytes {
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
		if digest(plaintext) != record.SHA256 {
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
		documents = append(documents, validatedDocument{ID: record.ID, Ciphertext: observedCiphertext(ciphertext)})
	}
	valid = true
	return planForValidatedSnapshot(validatedSnapshot{
		MarkerSHA256: digest(marker), DocumentsRootPresent: true,
		Ledger:    pointerCiphertext(observedCiphertext(ledgerCiphertext)),
		Documents: documents,
	}), snapshot, nil
}

func pointerCiphertext(value validatedCiphertext) *validatedCiphertext { return &value }

func validCiphertext(content []byte) bool {
	return len(content) > 0 && len(content) <= legacydpapi.MaxCiphertextBytes
}
