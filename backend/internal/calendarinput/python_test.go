package calendarinput

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// boundedBytes avoids promoted bytes.Buffer.ReadFrom bypassing the write cap.
type boundedBytes struct {
	buffer bytes.Buffer
	max    int
}

func (b *boundedBytes) Write(p []byte) (int, error) {
	if len(p) > b.max-b.buffer.Len() {
		return 0, errors.New("synthetic reference output limit")
	}
	return b.buffer.Write(p)
}

func TestLargeInputIsNotArtificiallyFormatCapped(t *testing.T) {
	for _, s := range []string{"\t" + strings.Repeat(" ", 100000) + "2026-01", "2026-01-01T12:30:40." + strings.Repeat("1", 100000)} {
		if v, e := NormalizeMonth(str(s)); e != nil || v != "2026-01" {
			t.Fatal("invented format cap")
		}
	}
}
