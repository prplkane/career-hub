package applications

import (
	"strings"
	"testing"
)

func TestSummaryTransactionAdmission(t *testing.T) {
	for _, test := range []struct {
		ids  []string
		code string
	}{
		{[]string{"\xff"}, "INPUT_UNSUPPORTED"},
		{[]string{strings.Repeat("x", MaxIDBytes+1)}, "INPUT_LIMIT"},
		{make([]string, MaxRequestedIDs+1), "INPUT_LIMIT"},
		{[]string{"job"}, "DATABASE_CONNECTION_INVALID"},
	} {
		got, e := SummariesInTransaction(nil, nil, test.ids)
		if got != nil || e == nil || e.Code != test.code {
			t.Fatal("summary transaction input admission changed")
		}
	}
	got, e := SummariesInTransaction(nil, nil, nil)
	if e != nil || got == nil || len(got) != 0 {
		t.Fatal("empty summary request touched transaction")
	}
}
