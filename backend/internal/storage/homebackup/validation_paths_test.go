package homebackup

import (
	"strings"
	"testing"

	"careerhub.local/backend/internal/vault/vaultread"
)

var reservedAliasFixtures = map[string]string{
	"vault-alias":              "career_vault/PROFILE.yaml",
	"job-directory-alias":      "jobs/Normalized/invalid.yaml",
	"document-state-alias":     "documents/KEY_STATE.json",
	"document-ledger-alias":    "documents/LEDGER.chdoc",
	"document-legacy-alias":    "documents/LEDGER.dpapi",
	"document-directory-alias": "documents/Files/invalid.chdoc",
	"database-alias":           "applications/CAREER_HUB.sqlite3",
	"database-wal-alias":       "applications/career_hub.sqlite3-WAL",
	"resume-directory-alias":   "applications/Resumes/library/invalid.pdf",
	"reserved-file-directory":  "documents/key_state.json/child.txt",
}

func TestPayloadReservedPathSpellings(t *testing.T) {
	for name, path := range reservedAliasFixtures {
		t.Run(name, func(t *testing.T) {
			// The canonical spelling need not also exist for an alias to be unsafe.
			if p := validatePayloadPaths([]File{testFile(path, "invalid")}); p == nil || p.Code != "BACKUP_PAYLOAD_PATH_INVALID" {
				t.Fatal("reserved alias accepted", path, p)
			}
		})
	}
	for _, path := range vaultread.SetupPaths() {
		if p := validatePayloadPaths([]File{testFile(strings.ToUpper(path), "invalid")}); p == nil {
			t.Fatal("Vault alias accepted", path)
		}
	}
	for _, path := range append(vaultread.SetupPaths(), databasePath, databasePath+"-wal", databasePath+"-shm", databasePath+"-journal",
		"documents/key_state.json", "documents/ledger.chdoc", "documents/ledger.dpapi", "documents/files/Example.chdoc",
		"jobs/normalized/Example.YAML", "jobs/raw_jd/Example.TXT", "applications/resumes/library/Example.PDF", "outputs/Résumé.txt") {
		if p := validatePayloadPaths([]File{testFile(path, "content")}); p != nil {
			t.Fatal("canonical path or user filename rejected", path, p)
		}
	}
}
