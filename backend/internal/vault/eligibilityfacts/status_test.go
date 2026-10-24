package eligibilityfacts

import "testing"

func TestQualifiedCurrentStatusLabels(t *testing.T) {
	for _, text := range []string{"OPT", "STEM OPT", "f1 opt", "F1 STEM OPT", "F-1 OPT", "F-1 (STEM OPT)", "  F-1\t(STEM OPT)\r\n"} {
		if confirmed, ambiguous := OPTStatus(text); !confirmed || ambiguous {
			t.Fatalf("confirmed label %q", text)
		}
	}
	for _, text := range []string{"Not OPT", "US citizen, formerly OPT", "OPT unknown", "F–1 OPT", "OPT,"} {
		if confirmed, ambiguous := OPTStatus(text); confirmed || !ambiguous {
			t.Fatalf("unconfirmed prose %q", text)
		}
	}
	for _, text := range []string{"", "US citizen", "OPTIMISTIC", "adopted citizen", "Option", "OPT_legacy"} {
		if confirmed, ambiguous := OPTStatus(text); confirmed || ambiguous {
			t.Fatalf("incidental substring %q", text)
		}
	}
}
