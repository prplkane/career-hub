package applicationwrite

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		return 0, os.ErrInvalid
	}
	return b.Buffer.Write(p)
}
func TestTimestampSafetyBoundaries(t *testing.T) {
	for _, test := range []struct{ input, code string }{
		{"\xff", "APPLICATION_LEDGER_UNICODE_UNSUPPORTED"},
		{strings.Repeat(" ", maxTimestampBytes+1), "APPLICATION_LEDGER_INPUT_LIMIT"},
		{"0001-01-01T00:00:00+01:00", "APPLICATION_LEDGER_TIMESTAMP_RANGE"},
		{"9999-12-31T23:59:59-01:00", "APPLICATION_LEDGER_TIMESTAMP_RANGE"},
	} {
		if value, err := normalizeTimestamp(test.input, ""); value != "" || err == nil || err.Code != test.code {
			t.Fatal("safety boundary", test.code)
		}
	}
}
