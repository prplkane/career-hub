package documentmigration

import (
	"bytes"
	"maps"
	"slices"
	"strings"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/legacydpapi"
	"careerhub.local/backend/internal/runtimepaths"
)

const legacyAbsentMarker = "\n"

type RecoveryAction string

const (
	RecoveryPreserveCandidate RecoveryAction = "preserve_candidate"
	RecoveryRestoreOriginal   RecoveryAction = "restore_original"
	RecoveryPreserveStaging   RecoveryAction = "preserve_staging"
	RecoveryRestoreMarker     RecoveryAction = "restore_marker"
	RecoveryDiscardKey        RecoveryAction = "discard_key"
	RecoveryWriteRecord       RecoveryAction = "write_recovery_record"
	RecoveryRemoveJournal     RecoveryAction = "remove_journal"
)

// RecoveryPlan is a read-only decision over rechecked, bounded observations.
// It contains no path or credential authority. A future writer must rebuild the
// facts under the same process claim before every irreversible transition.
type RecoveryPlan struct {
	Journal Journal
	Channel string
	Actions []RecoveryAction
}

type recoveryFacts struct {
	journal                     Journal
	channel                     string
	legacyOriginallyAbsent      bool
	originalPresent             bool
	originalMatchesRecovery     bool
	activePresent               bool
	activeMatchesRecovery       bool
	interruptedCandidatePresent bool
	stagePresent                bool
	interruptedStagingPresent   bool
	markerRestored              bool
}

type legacyTree struct {
	files       map[string]validatedCiphertext
	documents   int
	ciphertexts int
	totalBytes  int
}

// BuildRecoveryPlanOwned validates an active migration recovery layout and
// returns the exact source-compatible action order. Absence of the journal is
// a successful no-op. No filesystem or credential mutation is performed.
func BuildRecoveryPlanOwned(root *runtimepaths.Root) (RecoveryPlan, bool, *Error) {
	if root == nil {
		return RecoveryPlan{}, false, fail(CodeOwnershipRequired)
	}
	return buildRecoveryPlanOwned(root)
}

func buildRecoveryPlanOwned(root ownedTree) (RecoveryPlan, bool, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return RecoveryPlan{}, false, fail(CodeOwnershipRequired)
	}
	journalBytes, present, pathProblem := root.ReadFile(datahome.DocumentRecoveryPath, MaxJournalBytes)
	if pathProblem != nil {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	if !present {
		return RecoveryPlan{}, false, nil
	}
	journal, problem := ParseJournal(journalBytes)
	if problem != nil {
		return RecoveryPlan{}, false, problem
	}
	base := recoveryRelative(journal.MigrationID)
	completedPresent, pathProblem := root.Exists(base + "/completed.json")
	if pathProblem != nil || completedPresent {
		// Completion-before-journal-removal needs a separate active-v2 finalizer;
		// never reinterpret it as ordinary rollback.
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	recoveredBytes, recoveredPresent, pathProblem := root.ReadFile(base+"/recovered_journal.json", MaxJournalBytes)
	if pathProblem != nil {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	if recoveredPresent {
		expected, expectedProblem := (Journal{MigrationID: journal.MigrationID, State: JournalRecoveryVerified}).CanonicalBytes()
		if expectedProblem != nil || !bytes.Equal(recoveredBytes, expected) {
			return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
		}
	}

	marker, markerPresent, pathProblem := root.ReadFile(base+"/legacy_marker.json", datahome.MaxMarkerBytes)
	if pathProblem != nil || !markerPresent {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	markerInfo, markerProblem := datahome.ParseMarkerForDocumentMigration(marker)
	if markerProblem != nil || markerInfo.FormatVersion != SourceFormatVersion {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	activeMarker, activeMarkerPresent, pathProblem := root.ReadFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if pathProblem != nil || !activeMarkerPresent {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	markerRestored := bytes.Equal(activeMarker, marker)
	if !markerRestored {
		activeInfo, activeProblem := datahome.ParseMarkerForDocumentMigration(activeMarker)
		if activeProblem != nil || activeInfo.FormatVersion != TargetFormatVersion || activeInfo.Channel != markerInfo.Channel ||
			journal.DataHomeID == nil || activeInfo.DataHomeID != *journal.DataHomeID {
			return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
		}
		expected, markerBytesProblem := datahome.MarkerBytes(markerInfo.Channel, *journal.DataHomeID)
		if markerBytesProblem != nil || !bytes.Equal(activeMarker, expected) {
			return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
		}
	}
	manifestBytes, manifestPresent, pathProblem := root.ReadFile(base+"/recovery_manifest.json", MaxJournalBytes)
	if pathProblem != nil || !manifestPresent {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	manifest, manifestProblem := ParseRecoveryManifest(manifestBytes)
	if manifestProblem != nil {
		return RecoveryPlan{}, false, manifestProblem
	}
	absentBytes, absentPresent, pathProblem := root.ReadFile(base+"/legacy_documents_absent", int64(len(legacyAbsentMarker)))
	if pathProblem != nil || absentPresent && string(absentBytes) != legacyAbsentMarker ||
		absentPresent == manifest.DocumentsRootPresent {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	recoveryTree, problem := readLegacyTree(root, base+"/legacy_documents")
	if problem != nil || !manifestMatchesTree(manifest, recoveryTree) {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}

	originalPath := base + "/activation_original"
	_, originalPresent, pathProblem := root.ListDirectoryNames(originalPath)
	if pathProblem != nil {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	originalMatches := false
	if originalPresent {
		original, treeProblem := readLegacyTree(root, originalPath)
		if treeProblem != nil {
			return RecoveryPlan{}, false, treeProblem
		}
		originalMatches = equalLegacyTrees(original, recoveryTree)
	}

	_, activePresent, pathProblem := root.ListDirectoryNames("documents")
	if pathProblem != nil {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	activeMatches := false
	if activePresent && !originalPresent && manifest.DocumentsRootPresent {
		active, treeProblem := readLegacyTree(root, "documents")
		if treeProblem != nil {
			return RecoveryPlan{}, false, treeProblem
		}
		activeMatches = equalLegacyTrees(active, recoveryTree)
	}
	interruptedPresent, pathProblem := root.Exists(base + "/interrupted_candidate")
	if pathProblem != nil {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	stagePath := stageRelative(journal.MigrationID)
	_, stagePresent, pathProblem := root.ListDirectoryNames(stagePath)
	if pathProblem != nil {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}
	interruptedStagePresent, pathProblem := root.Exists(base + "/interrupted_staging")
	if pathProblem != nil {
		return RecoveryPlan{}, false, fail(CodeRecoveryFailed)
	}

	plan, problem := planRecovery(recoveryFacts{
		journal: journal, channel: markerInfo.Channel,
		legacyOriginallyAbsent: manifest.DocumentsRootPresent == false,
		originalPresent:        originalPresent, originalMatchesRecovery: originalMatches,
		activePresent: activePresent, activeMatchesRecovery: activeMatches,
		interruptedCandidatePresent: interruptedPresent,
		stagePresent:                stagePresent, interruptedStagingPresent: interruptedStagePresent,
		markerRestored: markerRestored,
	})
	if problem != nil {
		return RecoveryPlan{}, false, problem
	}
	if root.RequireProcessOwnership() != nil {
		return RecoveryPlan{}, false, fail(CodeOwnershipRequired)
	}
	return plan, true, nil
}

func planRecovery(facts recoveryFacts) (RecoveryPlan, *Error) {
	if _, problem := facts.journal.CanonicalBytes(); problem != nil ||
		facts.channel != "dev" && facts.channel != "stable" ||
		!validRecoveryJournalIdentity(facts.journal) {
		return RecoveryPlan{}, fail(CodeRecoveryFailed)
	}
	actions := make([]RecoveryAction, 0, 7)
	if facts.originalPresent {
		if !facts.originalMatchesRecovery || facts.activePresent && facts.interruptedCandidatePresent {
			return RecoveryPlan{}, fail(CodeRecoveryFailed)
		}
		if facts.activePresent {
			actions = append(actions, RecoveryPreserveCandidate)
		}
		actions = append(actions, RecoveryRestoreOriginal)
	} else if facts.legacyOriginallyAbsent {
		if facts.activePresent && facts.interruptedCandidatePresent {
			return RecoveryPlan{}, fail(CodeRecoveryFailed)
		}
		if facts.activePresent {
			actions = append(actions, RecoveryPreserveCandidate)
		}
	} else if !facts.activePresent || !facts.activeMatchesRecovery {
		return RecoveryPlan{}, fail(CodeRecoveryFailed)
	}
	if facts.stagePresent {
		if facts.interruptedStagingPresent {
			return RecoveryPlan{}, fail(CodeRecoveryFailed)
		}
		actions = append(actions, RecoveryPreserveStaging)
	}
	if !facts.markerRestored {
		actions = append(actions, RecoveryRestoreMarker)
		return RecoveryPlan{Journal: facts.journal, Channel: facts.channel, Actions: actions}, nil
	}
	if facts.journal.DataHomeID != nil && facts.journal.KeyID != nil {
		actions = append(actions, RecoveryDiscardKey)
	}
	actions = append(actions, RecoveryWriteRecord, RecoveryRemoveJournal)
	return RecoveryPlan{Journal: facts.journal, Channel: facts.channel, Actions: actions}, nil
}

func validRecoveryJournalIdentity(journal Journal) bool {
	if journal.State == JournalRecoveryVerified {
		return journal.DataHomeID == nil && journal.KeyID == nil
	}
	return journalOrder[journal.State] >= journalOrder[JournalKeyPlanned] && validJournalIdentity(journal)
}

func readLegacyTree(root ownedTree, base string) (legacyTree, *Error) {
	names, present, pathProblem := root.ListDirectoryNames(base)
	if pathProblem != nil || !present {
		return legacyTree{}, fail(CodeRecoveryFailed)
	}
	ledgerPresent, filesPresent := false, false
	for _, name := range names {
		switch name {
		case "ledger.dpapi":
			ledgerPresent = true
		case "files":
			filesPresent = true
		default:
			return legacyTree{}, fail(CodeRecoveryFailed)
		}
	}
	result := legacyTree{files: map[string]validatedCiphertext{}}
	if ledgerPresent {
		content, found, readProblem := root.ReadObjectFile(base+"/ledger.dpapi", legacydpapi.MaxCiphertextBytes)
		if readProblem != nil || !found {
			return legacyTree{}, fail(CodeRecoveryFailed)
		}
		result.files["ledger.dpapi"] = observedCiphertext(content)
		result.ciphertexts++
		result.totalBytes += len(content)
	}
	fileNames := []string{}
	if filesPresent {
		fileNames, present, pathProblem = root.ListDirectoryFileNames(base + "/files")
		if pathProblem != nil || !present {
			return legacyTree{}, fail(CodeRecoveryFailed)
		}
		for _, name := range fileNames {
			if !strings.HasSuffix(name, ".dpapi") {
				return legacyTree{}, fail(CodeRecoveryFailed)
			}
			content, found, readProblem := root.ReadObjectFile(base+"/files/"+name, legacydpapi.MaxCiphertextBytes)
			if readProblem != nil || !found {
				return legacyTree{}, fail(CodeRecoveryFailed)
			}
			result.files["files/"+name] = observedCiphertext(content)
			result.documents++
			result.ciphertexts++
			result.totalBytes += len(content)
		}
	}
	afterNames, afterPresent, pathProblem := root.ListDirectoryNames(base)
	if pathProblem != nil || !afterPresent || !slices.Equal(names, afterNames) {
		return legacyTree{}, fail(CodeRecoveryFailed)
	}
	if filesPresent {
		afterFiles, afterPresent, pathProblem := root.ListDirectoryFileNames(base + "/files")
		if pathProblem != nil || !afterPresent || !slices.Equal(fileNames, afterFiles) {
			return legacyTree{}, fail(CodeRecoveryFailed)
		}
	}
	return result, nil
}

func manifestMatchesTree(manifest RecoveryManifest, tree legacyTree) bool {
	if manifest.Plan.DocumentCount != tree.documents ||
		manifest.Plan.CiphertextFileCount != tree.ciphertexts ||
		manifest.Plan.TotalCiphertextBytes != tree.totalBytes {
		return false
	}
	return manifest.DocumentsRootPresent || tree.ciphertexts == 0
}

func equalLegacyTrees(left, right legacyTree) bool {
	return left.documents == right.documents && left.ciphertexts == right.ciphertexts &&
		left.totalBytes == right.totalBytes && maps.Equal(left.files, right.files)
}

func recoveryRelative(migrationID string) string {
	return "backups/document-encryption-migration/sec240_" + migrationID
}

func stageRelative(migrationID string) string {
	return ".documents-sec240-" + migrationID + ".staging"
}
