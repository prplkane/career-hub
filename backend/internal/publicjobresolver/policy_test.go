package publicjobresolver

import (
	"encoding/json"
	"net/netip"
	"os"
	"testing"
)

func TestPinnedPolicyContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/policy_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Reference string
		URLs      []struct{ Input, Output, Code, Stage string }
		Addresses []struct {
			Input  string
			Public bool
		}
	}
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if vectors.Reference != "3fd141c809e68dd2f2cc60ef325a98e665c6bdb1" {
		t.Fatal("unreviewed reference")
	}
	for _, v := range vectors.URLs {
		t.Run(v.Input, func(t *testing.T) {
			got, e := NormalizeURL(v.Input, "")
			if v.Code != "" {
				if e == nil || e.Code != v.Code || e.Stage != v.Stage {
					t.Fatalf("error=%v, expected %s/%s", e, v.Code, v.Stage)
				}
				return
			}
			if e != nil || got != v.Output {
				t.Fatalf("got %q/%v want %q", got, e, v.Output)
			}
		})
	}
	for _, v := range vectors.Addresses {
		t.Run(v.Input, func(t *testing.T) {
			a, err := netip.ParseAddr(v.Input)
			if err != nil {
				t.Fatal(err)
			}
			if got := publicAddress(a); got != v.Public {
				t.Fatalf("public=%v want %v", got, v.Public)
			}
		})
	}
}
func TestURLDowngrade(t *testing.T) {
	if _, e := NormalizeURL("http://careers.example.com/a", "https"); e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_REDIRECT_REJECTED" {
		t.Fatal(e)
	}
}
