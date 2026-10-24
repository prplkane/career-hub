package mailtransport

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestFramingGuardBoundaries(t *testing.T) {
	deep := strings.Repeat("(", maxProtocolDepth+1)
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"greeting", "* OK [CAPABILITY IMAP4rev1] ready\r\n", true},
		{"freeTextQuote", "* OK server says \"hello\r\n* 1 FETCH (BODY[] {40}\r\n" + strings.Repeat("(", 40) + ")\r\n", true},
		{"freeTextParen", "* OK maintenance (planned\r\n* 1 FETCH (BODY[] {40}\r\n" + strings.Repeat("(", 40) + ")\r\n", true},
		{"depthBoundary", "* 1 FETCH (BODYSTRUCTURE " + strings.Repeat("(", maxProtocolDepth-1) + strings.Repeat(")", maxProtocolDepth) + ")\r\n", true},
		{"depthOver", "* 1 FETCH (BODYSTRUCTURE " + deep, false},
		{"unnumberedFetch", "* FETCH (BODYSTRUCTURE " + deep, false},
		{"longNumericPrefix", "* " + strings.Repeat("0", 80) + "1 FETCH (BODYSTRUCTURE " + deep, false},
		{"shortLeadingZeros", "* 0001 FETCH (BODYSTRUCTURE " + deep, false},
		{"longFreeText", "* OK " + strings.Repeat("x", 100) + " unmatched \"(\r\n", true},
		{"longCapability", "* CAPABILITY IMAP4rev1 " + strings.Repeat("x", 100) + "\r\n", true},
		{"quoted", "* 1 FETCH (FLAGS (\"" + deep + "\\\"label\"))\r\n", true},
		{"literal", "* 1 FETCH (UID 9 BODY[] {40}\r\n" + strings.Repeat("(", 40) + ")\r\na1 OK done\r\n", true},
		{"literalMarkerInsideBody", "* 1 FETCH (UID 9 BODY[] {12}\r\n{1}\r\n((((((())\r\na1 OK done\r\n", true},
		{"emptyLiteral", "* 1 FETCH (UID 9 BODY[] {0}\r\n)\r\n", true},
		{"literalPlus", "* 1 FETCH (UID 9 BODY[] {1+}\r\nx)\r\n", true},
		{"literalOverflow", "* 1 FETCH (UID 9 BODY[] {10485761}\r\n", false},
		{"literalDigitsOverflow", "* 1 FETCH (UID 9 BODY[] {0000000000000", false},
		{"literalNoDigits", "* 1 FETCH (UID 9 BODY[] {}\r\n", false},
		{"literalBadClose", "* 1 FETCH (UID 9 BODY[] {1+x", false},
		{"literalBadCR", "* 1 FETCH (UID 9 BODY[] {1}x", false},
		{"literalBadLF", "* 1 FETCH (UID 9 BODY[] {1}\rx", false},
		{"quotedLineBreak", "* 1 FETCH (FLAGS (\"x\r\n", false},
		{"bareLF", "* OK ready\n", false},
		{"bareCR", "* OK ready\rx", false},
		{"lineBoundary", "* OK " + strings.Repeat("x", maxProtocolLine-7) + "\r\n", true},
		{"lineOver", "* OK " + strings.Repeat("x", maxProtocolLine-6) + "\r\n", false},
		{"textMarkerCannotMaskNextResponse", "* OK harmless {1000}\r\n* 1 FETCH (BODYSTRUCTURE " + deep, false},
		{"textMarkerCannotMaskHugeLine", "a1 NO failure {1000}\r\n" + strings.Repeat("x", maxProtocolLine+1), false},
		{"responsePrefixReset", "* CAPABILITY IMAP4rev1\r\na1 OK done\r\n* 1 FETCH (BODY[] {40}\r\n" + strings.Repeat("(", 40) + ")\r\n", true},
		{"lowercaseFetch", "* 12 fetch (BODY[] {40}\r\n" + strings.Repeat("(", 40) + ")\r\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every split exercises a marker/literal/quote crossing a Read boundary.
			steps := []int{1, 2, 3, 7, 64, len(tc.input)}
			for _, step := range steps {
				var g frameGuard
				valid := true
				for off := 0; off < len(tc.input) && valid; off += step {
					valid = g.accept([]byte(tc.input[off:min(off+step, len(tc.input))]))
				}
				if valid != tc.valid {
					t.Fatalf("chunk=%d got=%v want=%v", step, valid, tc.valid)
				}
			}
		})
	}
}

func TestFramingLiteralAllOctetsAndContinuation(t *testing.T) {
	body := bytes.Repeat([]byte{0, '\r', '\n', '(', ')', '{', '}', '"', '\\', 255}, 100)
	wire := append([]byte(fmt.Sprintf("* 1 FETCH (UID 5 BODY[] {%d}\r\n", len(body))), body...)
	wire = append(wire, []byte(" FLAGS (\\Seen))\r\n* 2 FETCH (BODYSTRUCTURE "+strings.Repeat("(", maxProtocolDepth+1))...)
	var g frameGuard
	prefixLength := len(wire) - len("* 2 FETCH (BODYSTRUCTURE ") - maxProtocolDepth - 1
	if !g.accept(wire[:prefixLength]) {
		t.Fatal("literal bytes were treated as grammar")
	}
	if g.accept(wire[prefixLength:]) {
		t.Fatal("next response escaped depth limit")
	}
}

func FuzzFrameGuardChunking(f *testing.F) {
	f.Add([]byte("* 1 FETCH (UID 5 BODY[] {4}\r\n\x00()x)\r\na1 OK done\r\n"))
	f.Add([]byte("* OK {9000}\r\n* 1 FETCH (BODYSTRUCTURE ((("))
	f.Fuzz(func(t *testing.T, data []byte) {
		var whole, split frameGuard
		a := whole.accept(data)
		b := true
		for _, v := range data {
			if !split.accept([]byte{v}) {
				b = false
				break
			}
		}
		if a != b {
			t.Fatal("framing result depends on network chunks")
		}
	})
}
