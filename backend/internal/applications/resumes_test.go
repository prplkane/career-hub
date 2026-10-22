package applications

import (
	"math"
	"strings"
	"testing"
)

func TestResumeRequestAndMalformedSizeBoundaries(t *testing.T) {
	for _, test := range []struct{ search, code string }{
		{"\xff", "INPUT_UNSUPPORTED"}, {strings.Repeat("x", MaxRequestBytes+1), "INPUT_LIMIT"},
	} {
		result, err := ReadResumeLibrary(nil, nil, ResumeFilters{Search: test.search})
		if result != nil || err == nil || err.Code != test.code {
			t.Fatal("invalid resume query reached database")
		}
		snapshot, err := ReadResumeManagementSnapshot(nil, nil, test.search)
		if snapshot != nil || err == nil || err.Code != test.code {
			t.Fatal("invalid management snapshot query reached database")
		}
	}
	for _, raw := range []any{nil, true, math.Inf(1), math.Inf(-1), math.NaN(), "-", "1__2", "1_", []byte{0xff}} {
		if _, err := resumeSize(raw); err == nil {
			t.Fatal("unsupported size accepted")
		}
	}
	if got, err := ReadResumeDigestIdentities(nil, nil); got != nil || err == nil {
		t.Fatal("missing read context/database accepted")
	}
	if got, err := ReadResumeManagementSnapshot(nil, nil, ""); got != nil || err == nil {
		t.Fatal("missing management read context/database accepted")
	}
}
