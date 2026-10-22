package readonly

import (
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applicationanalysis"
)

func TestScanIntakeIdentitiesChecksExactRetainedSource(t *testing.T) {
	data, _ := digestMemories()
	entries, err := scanIntakeIdentities(context.Background(), data, []string{"digest.yaml"})
	if err != nil || len(entries) != 1 || entries[0].JobID != "digest" || entries[0].postingIdentity != "url:https://example.test/job/1" {
		t.Fatal("valid existing job was not inspected", err)
	}
	fingerprint, err := applicationanalysis.SourceFingerprint(strings.TrimPrefix(strings.ReplaceAll(digestSource, "\r\n", "\n"), "\ufeff"))
	if err != nil || entries[0].fingerprint != fingerprint {
		t.Fatal("existing source fingerprint was not recomputed", err)
	}
	for _, step := range data.steps {
		_, path, _ := strings.Cut(step, ":")
		if path != digestCardPath && path != digestRawPath {
			t.Fatal("identity scan read unrelated private input", step)
		}
	}
	data, _ = digestMemories()
	data.files[digestCardPath] = append(data.files[digestCardPath], []byte("content_fingerprint: '"+strings.Repeat("a", 64)+"'\n")...)
	if got, err := scanIntakeIdentities(context.Background(), data, []string{"digest.yaml"}); got != nil || err == nil {
		t.Fatal("stale card fingerprint accepted", got, err)
	}
	data, _ = digestMemories()
	data.files[digestCardPath] = []byte(strings.ReplaceAll(digestCard, digestRawPath, "jobs/incoming/unrelated.txt"))
	if got, err := scanIntakeIdentities(context.Background(), data, []string{"digest.yaml"}); got != nil || err == nil {
		t.Fatal("misbound source accepted", got, err)
	}
	for _, step := range data.steps {
		if strings.Contains(step, "jobs/incoming/") {
			t.Fatal("misbound card caused an unrelated raw read", step)
		}
	}
	data, _ = digestMemories()
	data.after = func(operation, path string) {
		if operation == "read" && path == digestCardPath {
			data.files[path] = []byte(strings.ReplaceAll(digestCard, digestRawPath, "jobs/incoming/unrelated.txt"))
		}
	}
	if got, err := scanIntakeIdentities(context.Background(), data, []string{"digest.yaml"}); got != nil || err == nil {
		t.Fatal("card changed after raw-path preflight", got, err)
	}
	for _, step := range data.steps {
		if strings.Contains(step, "jobs/incoming/") {
			t.Fatal("changed card caused an unrelated raw read", step)
		}
	}
	data, _ = digestMemories()
	data.files["jobs/normalized/DIGEST.yml"] = []byte(strings.ReplaceAll(strings.ReplaceAll(digestCard, "id: digest", "id: DIGEST"), digestRawPath, "jobs/incoming/DIGEST.txt"))
	if got, err := scanIntakeIdentities(context.Background(), data, []string{"digest.yaml", "DIGEST.yml"}); got != nil || err == nil {
		t.Fatal("case-equivalent card IDs accepted", got, err)
	}
	data, _ = digestMemories()
	data.after = func(operation, path string) {
		if operation == "read" && path == digestRawPath {
			data.files[path] = []byte("changed source")
		}
	}
	if got, err := scanIntakeIdentities(context.Background(), data, []string{"digest.yaml"}); got != nil || err == nil {
		t.Fatal("changed retained source accepted", got, err)
	}
}

func TestDecideIntakeDuplicateKeepsRetryPostingAndContentDistinct(t *testing.T) {
	fingerprint := strings.Repeat("a", 64)
	entry := intakeIdentity{NewJobDuplicate: NewJobDuplicate{JobID: "first"},
		fingerprint: fingerprint, postingIdentity: "posting_id:req-1", retryKey: "retry-key-123"}
	for _, test := range []struct {
		name, fingerprint, posting, key, reason, code string
		existing                                      []intakeIdentity
	}{
		{"retry", fingerprint, "posting_id:other", "retry-key-123", "retry_key", "", []intakeIdentity{entry}},
		{"retry-conflict", strings.Repeat("b", 64), "", "retry-key-123", "", "JOB_INTAKE_RETRY_CONFLICT", []intakeIdentity{entry}},
		{"posting-beats-content", strings.Repeat("b", 64), "posting_id:req-1", "", "posting", "", []intakeIdentity{entry}},
		{"content", fingerprint, "", "", "content", "", []intakeIdentity{entry}},
		{"different-posting", fingerprint, "posting_id:req-2", "", "", "", []intakeIdentity{entry}},
		{"ambiguous", fingerprint, "", "", "", "JOB_INTAKE_DUPLICATE_AMBIGUOUS", []intakeIdentity{entry,
			{NewJobDuplicate: NewJobDuplicate{JobID: "second"}, fingerprint: fingerprint}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := decideIntakeDuplicate(test.fingerprint, test.posting, test.key, test.existing)
			if test.code != "" {
				requireCode(t, err, test.code)
				if got != nil {
					t.Fatal("error returned a duplicate")
				}
				return
			}
			if err != nil || test.reason == "" && got != nil || test.reason != "" && (got == nil || got.Reason != test.reason || got.JobID != "first") {
				t.Fatal("wrong duplicate decision", got, err)
			}
		})
	}
}
