//go:build windows

package readonly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
)

func TestNativeNewJobPreparationOwned(t *testing.T) {
	_, start := fixture(t)
	raw, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewJobPreparationProcessHelper$")
	cmd.Env = append(os.Environ(), "CAREERHUB_NEW_JOB_PREPARATION_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("owned new-JD preparation: deadline=%v exit=%v %.2000s", ctx.Err(), err, out)
	}
}

func TestNewJobPreparationProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_NEW_JOB_PREPARATION_HELPER") != "1" {
		return
	}
	var start protocol.StartRecord
	if json.NewDecoder(io.LimitReader(os.Stdin, 64<<10)).Decode(&start) != nil {
		t.Fatal("invalid synthetic start record")
	}
	workspace, err := OpenForProcess(start)
	if err != nil {
		t.Fatal("synthetic process admission", err)
	}
	data, resources := digestMemories()
	for path, raw := range data.files {
		if path != digestCardPath && path != digestRawPath {
			writeFixture(t, start.DataRoot, path, string(raw))
		}
	}
	for path, raw := range resources.files {
		writeFixture(t, start.ResourceRoot, path, string(raw))
	}
	base := filepath.Dir(start.DataRoot)
	before := tree(t, base)
	input := newJobInput()
	request := NewJobRequest{SourceName: "fictional_job.txt", RawText: input.RawText,
		SourceURL: input.SourceURL, IdempotencyKey: input.IdempotencyKey}
	selected, err := workspace.InspectNewJob(context.Background(), request, "Explicit company", "Explicit role")
	if err != nil || selected == nil || selected.Prepared == nil || selected.Duplicate != nil ||
		selected.Prepared.Destination.ID != "fictional_job" || selected.Prepared.Digest == nil {
		t.Fatal("native first destination", err)
	}
	got := selected.Prepared
	if got.RawFileSHA256 != sha256.Sum256([]byte(input.RawText)) ||
		got.PlannedCardSHA256 != sha256.Sum256(got.CardBytes) ||
		got.Digest.Fields["title"].Value != "Explicit role" {
		t.Fatal("native draft or witness mismatch")
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("read-only native preparation changed the synthetic Home")
	}
	existingSource := strings.ReplaceAll(digestSource, "Python and LocalTool.", "Different technology.")
	existingCard := strings.ReplaceAll(strings.ReplaceAll(digestCard, "id: digest", "id: fictional_job"), digestRawPath, "jobs/incoming/fictional_job.txt")
	writeFixture(t, start.DataRoot, "jobs/incoming/fictional_job.txt", existingSource)
	writeFixture(t, start.DataRoot, "jobs/normalized/fictional_job.yaml", existingCard)
	before = tree(t, base)
	collided, err := workspace.InspectNewJob(context.Background(), request, "Explicit company", "Explicit role")
	if err != nil || collided == nil || collided.Prepared == nil || collided.Duplicate != nil ||
		collided.Prepared.Destination.ID != "fictional_job_"+got.Digest.ContentFingerprint[:12] ||
		collided.Prepared.Destination.RawPath != "jobs/incoming/"+collided.Prepared.Destination.ID+".txt" {
		t.Fatal("native occupied destination", err)
	}
	postingRequest := request
	postingRequest.SourceURL, postingRequest.IdempotencyKey = "https://example.test/job/1", ""
	posting, err := workspace.InspectNewJob(context.Background(), postingRequest, "", "")
	if err != nil || posting == nil || posting.Prepared != nil || posting.Duplicate == nil ||
		posting.Duplicate.JobID != "fictional_job" || posting.Duplicate.Reason != "posting" {
		t.Fatal("native posting duplicate not recognized", err)
	}
	request.RawText, request.SourceURL, request.IdempotencyKey = existingSource, "", ""
	duplicate, err := workspace.InspectNewJob(context.Background(), request, "", "")
	if err != nil || duplicate == nil || duplicate.Prepared != nil || duplicate.Duplicate == nil ||
		duplicate.Duplicate.JobID != "fictional_job" || duplicate.Duplicate.Reason != "content" {
		t.Fatal("native source duplicate not recognized", err)
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("native identity/collision scan changed the synthetic Home")
	}
	writeFixture(t, start.DataRoot, "jobs/normalized/fictional_job.yaml", existingCard+"intake_idempotency_key: fictional-key-123\n")
	before = tree(t, base)
	request.RawText, request.IdempotencyKey = digestSource, "fictional-key-123"
	if result, err := workspace.InspectNewJob(context.Background(), request, "", ""); result != nil {
		t.Fatal("native conflicting retry returned a result")
	} else {
		requireCode(t, err, "JOB_INTAKE_RETRY_CONFLICT")
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("native retry conflict changed the synthetic Home")
	}
	if err := workspace.VerifyNewJobReadSet(context.Background(), got); err != nil {
		t.Fatal("fresh read set refused before journal", err)
	}
	writeFixture(t, start.DataRoot, datahome.NewJobRecoveryPath, "opaque synthetic pending intent")
	before = tree(t, base)
	if err := workspace.VerifyNewJobReadSet(context.Background(), got); err != nil {
		t.Fatal("owned read set could not run with a pending new-job intent", err)
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("read-set verification mutated a pending Home")
	}
	writeFixture(t, start.ResourceRoot, catalogstore.CoreSkillsPath, "[]\n")
	if err := workspace.VerifyNewJobReadSet(context.Background(), got); err == nil {
		t.Fatal("changed core catalog passed a prepared witness")
	}
	writeFixture(t, start.ResourceRoot, catalogstore.CoreSkillsPath, string(resources.files[catalogstore.CoreSkillsPath]))
	writeFixture(t, start.DataRoot, digestProfilePath, "current_status: changed\n")
	if err := workspace.VerifyNewJobReadSet(context.Background(), got); err == nil {
		t.Fatal("changed eligibility profile passed a prepared witness")
	}
}
