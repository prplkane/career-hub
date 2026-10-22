package readonly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/jobcard"
)

func newJobInput() jobcard.IntakeDraftInput {
	return jobcard.IntakeDraftInput{ID: "fictional_job", RawPath: "jobs/incoming/fictional_job.txt",
		RawText: digestSource, SourceURL: "https://example.test/jobs/fictional", IdempotencyKey: "fictional-key-123"}
}

func TestPrepareNewJobUsesOnlyOwnedCatalogAndProfileReadSet(t *testing.T) {
	data, resources := digestMemories()
	input := newJobInput()
	beforeData, beforeResources := map[string][]byte{}, map[string][]byte{}
	for path, raw := range data.files {
		beforeData[path] = bytes.Clone(raw)
	}
	for path, raw := range resources.files {
		beforeResources[path] = bytes.Clone(raw)
	}
	prepared, err := prepareNewJob(context.Background(), data, resources, input, "Explicit company", "Explicit role")
	if err != nil || prepared == nil {
		t.Fatal("new JD was not prepared", err)
	}
	if prepared.RawFileSHA256 != sha256.Sum256([]byte(input.RawText)) || prepared.PlannedCardSHA256 != sha256.Sum256(prepared.CardBytes) ||
		prepared.ProfileFileSHA256 != sha256.Sum256(beforeData[digestProfilePath]) || !prepared.ProfilePresent || prepared.CatalogRevision == "" ||
		!validNewJobReadSet(prepared) {
		t.Fatal("prepared bytes or dependency witnesses changed")
	}
	card, parseErr := cataloginput.ParseYAML(prepared.CardBytes)
	if parseErr != nil || card.Kind != "mapping" || prepared.Digest.Fields["company"].State != "provided" ||
		prepared.Digest.Fields["title"].Value != "Explicit role" || !bytes.Contains(prepared.CardBytes, []byte("fictional_job")) {
		t.Fatal("Go-v12 prepared card or explicit hints missing", parseErr)
	}
	if fmt.Sprintf("%+v", prepared) != "[redacted new job preparation]" {
		t.Fatal("private draft was printable")
	}
	if fmt.Sprintf("%+v", NewJobRequest{RawText: "private fictional JD"}) != "[redacted new job request]" {
		t.Fatal("private request was printable")
	}
	if fmt.Sprintf("%+v", NewJobInspection{Prepared: prepared}) != "[redacted new job inspection]" {
		t.Fatal("private inspection was printable")
	}
	if !sameByteFiles(beforeData, data.files) || !sameByteFiles(beforeResources, resources.files) {
		t.Fatal("new-JD preparation mutated the synthetic inputs")
	}
	allowedData := map[string]bool{digestProfilePath: true, catalogstore.CustomGroupsPath: true, catalogstore.CustomSkillsPath: true}
	allowedResources := map[string]bool{catalogstore.CoreGroupsPath: true, catalogstore.CoreSkillsPath: true}
	for _, observed := range []struct {
		steps   []string
		allowed map[string]bool
	}{{data.steps, allowedData}, {resources.steps, allowedResources}} {
		for _, step := range observed.steps {
			_, path, _ := strings.Cut(step, ":")
			if !observed.allowed[path] {
				t.Fatal("new intake opened an unrelated input", step)
			}
		}
	}
}

func TestPrepareNewJobRejectsInvalidOrChangedInputWithoutPartialCard(t *testing.T) {
	data, resources := digestMemories()
	invalid := newJobInput()
	invalid.RawText = " \x00 "
	if got, err := prepareNewJob(context.Background(), data, resources, invalid, "", ""); got != nil || err == nil {
		t.Fatal("invalid source produced a card", got, err)
	}
	if len(data.steps)+len(resources.steps) != 0 {
		t.Fatal("invalid source read private files")
	}
	data, resources = digestMemories()
	data.after = func(operation, path string) {
		if operation == "read" && path == digestProfilePath {
			data.files[digestProfilePath] = []byte("current_status: changed\n")
		}
	}
	if got, err := prepareNewJob(context.Background(), data, resources, newJobInput(), "", ""); got != nil || err == nil {
		t.Fatal("changed eligibility input returned a draft", got, err)
	}
	data, resources = digestMemories()
	resources.after = func(operation, path string) {
		if operation == "read" && path == catalogstore.CoreSkillsPath {
			resources.files[path] = []byte("[]\n")
		}
	}
	if got, err := prepareNewJob(context.Background(), data, resources, newJobInput(), "", ""); got != nil || err == nil {
		t.Fatal("changed core catalog returned a draft", got, err)
	}
}

func sameByteFiles(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for path, raw := range left {
		if !bytes.Equal(raw, right[path]) {
			return false
		}
	}
	return true
}
