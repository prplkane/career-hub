//go:build windows

package readonly

import (
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/skills/catalogstore"
)

func TestIntakeApprovalPreviewReadSetAndDrift(t *testing.T) {
	for _, scenario := range []string{"success", "vault drift", "core drift", "overlay appears"} {
		t.Run(scenario, func(t *testing.T) {
			data, resources := digestMemories()
			for i, path := range intakereview.ApprovalPreviewPaths() {
				raw := "[]\n"
				if i < 2 {
					raw = "{}\n"
				}
				data.files[path] = []byte(raw)
			}
			// A category-only block is structurally ready, without approving anything.
			review := strings.Replace(intakeViewReview(), `"block_id":"b1"}}`, `"block_id":"b1"},"category":"other","disposition":"categorized"}`, 1)
			path := intakereview.ApprovalPreviewPaths()[0]
			switch scenario {
			case "vault drift":
				data.after = func(op, p string) {
					if op == "read" && p == path {
						data.files[path] = []byte("name: changed\n")
					}
				}
			case "core drift":
				resources.after = func(op, p string) {
					if op == "read" && p == catalogstore.CoreSkillsPath {
						resources.files[p] = append(resources.files[p], []byte("\n# changed")...)
					}
				}
			case "overlay appears":
				delete(data.files, catalogstore.CustomSkillsPath)
				data.after = func(op, p string) {
					if op == "read" && p == catalogstore.CustomSkillsPath {
						data.files[p] = []byte("[]\n")
					}
				}
			}
			plan, err := prepareIntakeApprovalPreview(context.Background(), []byte(intakeViewExtraction()), []byte(review), data, resources)
			if scenario == "success" {
				if err != nil || plan == nil || len(plan.CategoryOnlyBlocks) != 1 {
					t.Fatal("preview unavailable", err)
				}
			} else if err == nil || plan != nil {
				t.Fatal("changed source escaped", scenario)
			}
			allowed := map[string]bool{catalogstore.CustomSkillsPath: true, catalogstore.CustomGroupsPath: true}
			for _, path := range intakereview.ApprovalPreviewPaths() {
				allowed[path] = true
			}
			for _, step := range data.steps {
				_, path, _ := strings.Cut(step, ":")
				if !allowed[path] {
					t.Fatal("unrelated private input read", path)
				}
			}
		})
	}
}
