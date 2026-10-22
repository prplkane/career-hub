package textcompat

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestPinnedUpperAndDecimalMappings(t *testing.T) {
	upper, digits := sha256.New(), sha256.New()
	for point := rune(0); point <= 0x10ffff; point++ {
		if point < 0xd800 || point > 0xdfff {
			if input, output := string(point), Upper(string(point)); output != input {
				_, _ = fmt.Fprintf(upper, "%x:%s\n", point, output)
			}
		}
		digit, found := DecimalDigit(point)
		var encoded byte
		if found {
			encoded = byte(digit + 1)
		}
		_, _ = digits.Write([]byte{encoded})
	}
	if fmt.Sprintf("%x", upper.Sum(nil)) != "b648162bdf8db291c9414eec55ad0b05450c25a41dbffbd5cf28c72681ec79ab" ||
		fmt.Sprintf("%x", digits.Sum(nil)) != "63786f7ca550846ae22618aeff8eb7c9dcd25b8aae895b6bf8276ce90abec8b5" {
		t.Fatal("pinned Python3.12.14 Unicode15 mapping changed")
	}
	if Upper("Résumé.ßﬃ") != "RÉSUMÉ.SSFFI" || Upper("\u1c89") != "\u1c89" {
		t.Fatal("full uppercase expansion or post-Unicode15 stability changed")
	}
}
