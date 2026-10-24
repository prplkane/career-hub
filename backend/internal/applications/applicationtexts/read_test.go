package applicationtexts

import (
	"context"
	"strings"
	"testing"
)

func TestParseWorkbenchReadIdentityAndLegacyValues(t *testing.T) {
	base := "schema_version: 1\njob_id: job\nitems: [{id: answer, kind: short_answer, state: submitted, response: {z: 1, a: 2}}]\nobservations: [{date: 2026-09-27}]\n"
	fp := "fingerprint"
	for _, test := range []struct {
		raw   string
		fp    *string
		valid bool
	}{
		{base, nil, true}, {base + "jd_fingerprint: null\n", nil, true},
		{base + "jd_fingerprint: fingerprint\n", &fp, true},
		{strings.Replace(base, "schema_version: 1", "schema_version: true", 1), nil, true},
		{base, &fp, false}, {base + "jd_fingerprint: stale\n", &fp, false},
		{base + "jd_fingerprint: fingerprint\n", nil, false},
		{strings.Replace(base, "job_id: job", "job_id: another", 1), nil, false},
		{strings.Replace(base, "kind: short_answer", "kind: unknown", 1), nil, false},
		{strings.Replace(base, "state: submitted", "state: unknown", 1), nil, false},
		{"{}", nil, false}, {"bad: [", nil, false},
	} {
		got, e := ParseWorkbench(context.Background(), []byte(test.raw), "job", test.fp)
		if test.valid {
			if e != nil || field(field(got, "items").Items[0], "response").Kind != "mapping" || field(field(got, "observations").Items[0], "date").Kind != "date" {
				t.Fatal("read changed legacy values or identity", e)
			}
		} else if e == nil || got.Kind != "" {
			t.Fatal("invalid workbench returned data")
		}
	}
	if got, e := ParseWorkbench(context.Background(), []byte(strings.Repeat("x", MaxStoreBytes+1)), "job", nil); got.Kind != "" || e == nil || e.Code != "APPLICATION_TEXT_STORE_LIMIT" {
		t.Fatal("unbounded store accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, e := ParseWorkbench(ctx, []byte(base), "job", nil); got.Kind != "" || e == nil {
		t.Fatal("canceled parser returned data")
	}
}
