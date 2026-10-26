package documentmigration

import (
	"runtime"
	"slices"
	"strings"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/legacydpapi"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const (
	CodeRecoveryRequired  = "DOCUMENT_MIGRATION_RECOVERY_REQUIRED"
	CodePathInvalid       = "DOCUMENT_MIGRATION_PATH_INVALID"
	CodeOwnershipRequired = "DATA_HOME_OWNERSHIP_REQUIRED"

	legacyLedgerPath = "documents/ledger.dpapi"
	legacyFilesPath  = "documents/files"
)

type ownedTree interface {
	RequireProcessOwnership() *runtimepaths.Error
	Exists(string) (bool, *runtimepaths.Error)
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
	ReadObjectFile(string, int64) ([]byte, bool, *runtimepaths.Error)
	ListDirectoryNames(string) ([]string, bool, *runtimepaths.Error)
	ListDirectoryFileNames(string) ([]string, bool, *runtimepaths.Error)
}

// BuildPlanOwned reads and validates one legacy source while the dedicated
// maintenance process already owns the Data Home. It is read-only and does not
// claim ownership, recover a journal, create a key, or stage/replace files.
func BuildPlanOwned(root *runtimepaths.Root) (Plan, *Error) {
	return buildPlanOwned(root, runtime.GOOS == "windows", legacydpapi.UnprotectLegacy)
}

func buildPlanOwned(root ownedTree, windows bool, unprotect unprotectFunc) (Plan, *Error) {
	plan, snapshot, problem := readSnapshotOwned(root, windows, unprotect)
	snapshot.clear()
	return plan, problem
}

// readSnapshotOwned retains the exact validated legacy bytes for the bounded
// writer. The returned snapshot owns sensitive buffers; a successful caller
// must defer clear immediately. Failure always returns an empty snapshot.
func readSnapshotOwned(root ownedTree, windows bool, unprotect unprotectFunc) (plan Plan, snapshot legacySourceSnapshot, problem *Error) {
	return readSnapshotOwnedWithRecovery(root, windows, unprotect, false)
}

func readSnapshotOwnedWithRecovery(root ownedTree, windows bool, unprotect unprotectFunc,
	recoveryExpected bool) (plan Plan, snapshot legacySourceSnapshot, problem *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return Plan{}, legacySourceSnapshot{}, fail(CodeOwnershipRequired)
	}
	recovery, pathError := root.Exists(datahome.DocumentRecoveryPath)
	if pathError != nil {
		return Plan{}, legacySourceSnapshot{}, pathFailure(pathError, false)
	}
	if recovery && !recoveryExpected {
		return Plan{}, legacySourceSnapshot{}, fail(CodeRecoveryRequired)
	}
	if !recovery && recoveryExpected {
		return Plan{}, legacySourceSnapshot{}, fail(CodeRecoveryFailed)
	}
	marker, present, pathError := root.ReadFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if pathError != nil {
		return Plan{}, legacySourceSnapshot{}, markerFailure(pathError)
	}
	if !present {
		return Plan{}, legacySourceSnapshot{}, fail(CodePathInvalid)
	}
	info, markerError := datahome.ParseMarkerForDocumentMigration(marker)
	if markerError != nil {
		return Plan{}, legacySourceSnapshot{}, fail(CodeDataHomeInvalid)
	}
	restore, pathError := root.Exists(datahome.RestoreRecoveryPath)
	if pathError != nil {
		return Plan{}, legacySourceSnapshot{}, pathFailure(pathError, false)
	}
	if restore {
		return Plan{}, legacySourceSnapshot{}, fail(CodeDataHomeInvalid)
	}
	if info.FormatVersion != SourceFormatVersion {
		return Plan{}, legacySourceSnapshot{}, fail(CodeNotRequired)
	}
	documentNames, documentsPresent, pathError := root.ListDirectoryNames("documents")
	if pathError != nil {
		return Plan{}, legacySourceSnapshot{}, pathFailure(pathError, true)
	}
	ledgerPresent, filesPresent := false, false
	for _, name := range documentNames {
		switch name {
		case "ledger.dpapi":
			ledgerPresent = true
		case "files":
			filesPresent = true
		default:
			return Plan{}, legacySourceSnapshot{}, fail(CodeSourceInvalid)
		}
	}
	var ledgerCiphertext []byte
	if ledgerPresent {
		ledgerCiphertext, present, pathError = root.ReadObjectFile(legacyLedgerPath, legacydpapi.MaxCiphertextBytes)
		if pathError != nil {
			return Plan{}, legacySourceSnapshot{}, pathFailure(pathError, true)
		}
		if !present {
			return Plan{}, legacySourceSnapshot{}, fail(CodeSourceInvalid)
		}
	}
	fileNames := []string{}
	if filesPresent {
		fileNames, present, pathError = root.ListDirectoryFileNames(legacyFilesPath)
		if pathError != nil {
			return Plan{}, legacySourceSnapshot{}, pathFailure(pathError, true)
		}
		if !present {
			return Plan{}, legacySourceSnapshot{}, fail(CodeSourceInvalid)
		}
	}
	plan, snapshot, problem = validateSourceSnapshot(marker, documentsPresent, ledgerPresent,
		ledgerCiphertext, fileNames, windows,
		func(name string) ([]byte, *Error) {
			content, present, pathError := root.ReadObjectFile(
				legacyFilesPath+"/"+name, legacydpapi.MaxCiphertextBytes)
			if pathError != nil {
				return nil, pathFailure(pathError, true)
			}
			if !present {
				return nil, fail(CodeSourceInvalid)
			}
			return content, nil
		}, unprotect)
	if problem != nil {
		return Plan{}, legacySourceSnapshot{}, problem
	}
	defer func() {
		if problem != nil {
			plan = Plan{}
			snapshot.clear()
		}
	}()
	// Re-list membership after content reads. This does not create an atomic
	// snapshot, but it refuses ordinary cooperating drift within the owned pass.
	afterDocuments, afterPresent, pathError := root.ListDirectoryNames("documents")
	if pathError != nil {
		return Plan{}, snapshot, pathFailure(pathError, true)
	}
	if afterPresent != documentsPresent || !slices.Equal(afterDocuments, documentNames) {
		return Plan{}, snapshot, fail(CodeSourceInvalid)
	}
	afterFiles := []string{}
	if filesPresent {
		afterFiles, present, pathError = root.ListDirectoryFileNames(legacyFilesPath)
		if pathError != nil {
			return Plan{}, snapshot, pathFailure(pathError, true)
		}
		if !present || !slices.Equal(afterFiles, fileNames) {
			return Plan{}, snapshot, fail(CodeSourceInvalid)
		}
	}
	if root.RequireProcessOwnership() != nil {
		return Plan{}, snapshot, fail(CodeOwnershipRequired)
	}
	return plan, snapshot, nil
}

func markerFailure(problem *runtimepaths.Error) *Error {
	if problem != nil {
		switch problem.Code {
		case "FILE_SIZE_LIMIT", "PATH_NOT_REGULAR", "FILE_OPEN", "FILE_READ", "FILE_STAT":
			return fail(CodeDataHomeInvalid)
		}
	}
	return fail(CodePathInvalid)
}

func pathFailure(problem *runtimepaths.Error, structural bool) *Error {
	if structural && problem != nil &&
		(strings.HasPrefix(problem.Code, "PATH_NOT_") || problem.Code == "FILE_SIZE_LIMIT") {
		return fail(CodeSourceInvalid)
	}
	return fail(CodePathInvalid)
}
