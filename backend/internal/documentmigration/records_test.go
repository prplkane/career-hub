package documentmigration

import (
	"strings"
	"testing"
)

func fixturePlan() Plan {
	return Plan{
		PlanID: "sha256:" + strings.Repeat("a", 64), SourceFormatVersion: 1, TargetFormatVersion: 2,
		DocumentCount: 1, CiphertextFileCount: 2, TotalCiphertextBytes: 52,
	}
}

func TestRecoveryManifestAndCompletionCanonicalRoundTrip(t *testing.T) {
	manifest := RecoveryManifest{Plan: fixturePlan(), DocumentsRootPresent: true}
	content, problem := manifest.CanonicalBytes()
	want := `{"ciphertext_file_count":2,"document_count":1,"documents_root_present":true,"plan_id":"sha256:` + strings.Repeat("a", 64) + `","source_format_version":1,"target_format_version":2,"total_ciphertext_bytes":52,"version":1}` + "\n"
	if problem != nil || string(content) != want {
		t.Fatalf("manifest = %q / %#v", content, problem)
	}
	parsed, problem := ParseRecoveryManifest(content)
	if problem != nil || parsed != manifest {
		t.Fatalf("manifest round trip = %+v / %#v", parsed, problem)
	}

	completed := CompletionRecord{fixturePlan().PlanID, strings.Repeat("b", 32), 1}
	content, problem = completed.CanonicalBytes()
	parsedCompleted, problem := ParseCompletionRecord(content)
	if problem != nil || parsedCompleted != completed {
		t.Fatalf("completion round trip = %+v / %#v", parsedCompleted, problem)
	}
}

func TestRecoveryRecordsRejectNoncanonicalOrImpossibleFacts(t *testing.T) {
	manifest := RecoveryManifest{Plan: fixturePlan(), DocumentsRootPresent: true}
	content, _ := manifest.CanonicalBytes()
	for index, candidate := range [][]byte{
		[]byte(" " + string(content)),
		[]byte(strings.Replace(string(content), `"version":1`, `"extra":0,"version":1`, 1)),
		[]byte(strings.Replace(string(content), `"ciphertext_file_count":2`, `"ciphertext_file_count":3`, 1)),
		[]byte(strings.Replace(string(content), `"plan_id":"sha256:a`, `"plan_id":"sha256:A`, 1)),
	} {
		if parsed, problem := ParseRecoveryManifest(candidate); parsed != (RecoveryManifest{}) || problem == nil || problem.Code != CodeRecoveryFailed {
			t.Fatalf("manifest case %d accepted: %+v / %#v", index, parsed, problem)
		}
	}
	bad := manifest
	bad.Plan.CiphertextFileCount = 1
	if content, problem := bad.CanonicalBytes(); content != nil || problem == nil {
		t.Fatalf("impossible plan encoded: %q / %#v", content, problem)
	}
	completed, _ := (CompletionRecord{fixturePlan().PlanID, strings.Repeat("b", 32), 1}).CanonicalBytes()
	if parsed, problem := ParseCompletionRecord([]byte(strings.Replace(string(completed), "completed", "pending", 1))); parsed != (CompletionRecord{}) || problem == nil {
		t.Fatalf("invalid completion accepted: %+v / %#v", parsed, problem)
	}
}

func TestJournalAllowsOnlyOneIdentityBoundForwardStep(t *testing.T) {
	home, key := "dh_0123456789abcdef0123456789abcdef", "key_0123456789abcdef0123456789abcdef"
	migration := strings.Repeat("c", 32)
	recovery := Journal{MigrationID: migration, State: JournalRecoveryVerified}
	planned := Journal{MigrationID: migration, State: JournalKeyPlanned, DataHomeID: &home, KeyID: &key}
	created := Journal{MigrationID: migration, State: JournalKeyCreated, DataHomeID: &home, KeyID: &key}
	if !recovery.CanAdvanceTo(planned) || !planned.CanAdvanceTo(created) {
		t.Fatal("legal journal transition refused")
	}
	other := "key_11111111111111111111111111111111"
	wrongIdentity := created
	wrongIdentity.KeyID = &other
	if recovery.CanAdvanceTo(created) || planned.CanAdvanceTo(wrongIdentity) || planned.CanAdvanceTo(planned) {
		t.Fatal("illegal journal transition accepted")
	}
}
