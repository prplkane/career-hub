package textcompat

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestPinnedPrintableProperty(t *testing.T) {
	hash := sha256.New()
	count := 0
	for point := rune(0); point <= 0x10ffff; point++ {
		value := byte(0)
		if IsPrintable(point) {
			value = 1
			count++
		}
		_, _ = hash.Write([]byte{value})
	}
	if count != 148998 || fmt.Sprintf("%x", hash.Sum(nil)) != "a24a109bc8e347f9490466c59e4160d0dc9dd1006a5be9a4c91af71cd2e7fa99" {
		t.Fatal("Python3.12.14 Unicode15 printable fingerprint changed")
	}
	for _, point := range []rune{-1, 0x110000, 0xd800, 0x1c89, 0xad, 0xa0} {
		if IsPrintable(point) {
			t.Fatal("invalid/nonprintable/post-Unicode15 code point accepted")
		}
	}
	for index, pair := range printableRanges {
		if pair[0] > pair[1] || index > 0 && printableRanges[index-1][1]+1 >= pair[0] {
			t.Fatal("printable ranges are not canonical disjoint runs")
		}
	}
}
