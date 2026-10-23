//go:build windows

package applicationwrite

import (
	"fmt"
	"strings"
	"testing"
)

func TestContactFieldsDiagnosticFormatting(t *testing.T) {
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, ContactFields{"name": "private-name", "channels": []any{"private-email"}}), "private-") {
			t.Fatal("private contact formatted")
		}
	}
}
