package documentmigration

import (
	"strings"
	"testing"
)

func stringPointer(value string) *string { return &value }

func TestJournalCanonicalBytesAndRoundTrip(t *testing.T) {
	home := "dh_0123456789abcdef0123456789abcdef"
	key := "key_0123456789abcdef0123456789abcdef"
	for _, state := range []JournalState{
		JournalRecoveryVerified, JournalKeyPlanned, JournalKeyCreated,
		JournalStagingVerified, JournalDocumentsSwapped, JournalMarkerActivated,
	} {
		journal := Journal{MigrationID: strings.Repeat("a", 32), State: state}
		if state != JournalRecoveryVerified {
			journal.DataHomeID, journal.KeyID = &home, &key
		}
		content, problem := journal.CanonicalBytes()
		if problem != nil {
			t.Fatal(problem.Code)
		}
		parsed, problem := ParseJournal(content)
		if problem != nil || parsed.MigrationID != journal.MigrationID || parsed.State != state ||
			!sameOptional(parsed.DataHomeID, journal.DataHomeID) || !sameOptional(parsed.KeyID, journal.KeyID) {
			t.Fatalf("round trip failed: %#v / %#v", parsed, problem)
		}
	}

	journal := Journal{
		MigrationID: strings.Repeat("b", 32), State: JournalKeyPlanned,
		DataHomeID: stringPointer("home<é>"), KeyID: stringPointer("key\x7f"),
	}
	content, problem := journal.CanonicalBytes()
	want := `{"data_home_id":"home<\u00e9>","key_id":"key\u007f","migration_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","state":"key_planned","version":1}` + "\n"
	if problem != nil || string(content) != want {
		t.Fatalf("canonical journal = %q / %#v", content, problem)
	}
}

func TestJournalRejectsNoncanonicalOrInvalidBytes(t *testing.T) {
	valid, problem := (Journal{
		MigrationID: strings.Repeat("a", 32), State: JournalRecoveryVerified,
	}).CanonicalBytes()
	if problem != nil {
		t.Fatal(problem.Code)
	}
	invalid := [][]byte{
		nil,
		[]byte(strings.TrimSpace(string(valid))),
		[]byte(" " + string(valid)),
		[]byte(strings.Replace(string(valid), `"version":1`, `"extra":0,"version":1`, 1)),
		[]byte(strings.Replace(string(valid), `"version":1`, `"version":1,"version":1`, 1)),
		[]byte(strings.Replace(string(valid), strings.Repeat("a", 32), strings.Repeat("A", 32), 1)),
		[]byte(strings.Replace(string(valid), "recovery_verified", "unknown", 1)),
		append([]byte(nil), append(valid, 0xff)...),
		[]byte(strings.Repeat(" ", MaxJournalBytes+1)),
	}
	for index, content := range invalid {
		if parsed, failure := ParseJournal(content); parsed != (Journal{}) || failure == nil || failure.Code != CodeRecoveryFailed {
			t.Fatalf("case %d accepted: %#v / %#v", index, parsed, failure)
		}
	}
	if content, failure := (Journal{MigrationID: "bad", State: JournalRecoveryVerified}).CanonicalBytes(); content != nil || failure == nil || failure.Code != CodeRecoveryFailed {
		t.Fatalf("invalid journal encoded: %q / %#v", content, failure)
	}
}

func sameOptional(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
