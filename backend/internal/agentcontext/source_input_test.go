package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSourceEvidenceRefusals(t *testing.T) {
	for _, request := range []SourceRequest{{}, {Text: " \u0085 "}, {Text: string([]byte{0xff})}, {Text: "valid", Title: strings.Repeat("x", 4097)}} {
		d, r := savedInputFixture()
		got, e := ReadSourceEvidence(context.Background(), d, r, request, savedInputDate)
		if got != nil || e == nil || len(d.reads) != 0 {
			t.Fatal("invalid source reached storage")
		}
	}
	request := SourceRequest{Text: "PRIVATE-marker\nPython services"}
	if strings.Contains(fmt.Sprintf("%+v", request), "PRIVATE-marker") {
		t.Fatal("request formatting leak")
	}
	for _, mode := range []string{"cancel", "drift", "invalid-vault", "denied"} {
		t.Run(mode, func(t *testing.T) {
			d, r := savedInputFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "cancel":
				cancel()
			case "drift":
				d.before = func(path string, n int) {
					if path == "career_vault/profile.yaml" && n == 2 {
						d.files[path] += "\n# concurrent edit"
					}
				}
			case "invalid-vault":
				d.files["career_vault/profile.yaml"] = "[]"
			case "denied":
				d.deny = "career_vault/profile.yaml"
			}
			got, e := ReadSourceEvidence(ctx, d, r, request, savedInputDate)
			if got != nil || e == nil {
				t.Fatal("unsafe advisory read accepted")
			}
		})
	}
}
