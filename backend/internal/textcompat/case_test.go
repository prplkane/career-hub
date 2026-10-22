package textcompat

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestPinnedLowerTitleAndIgnorable(t *testing.T) {
	lower, title, ignorable := sha256.New(), sha256.New(), sha256.New()
	for point := rune(0); point <= 0x10ffff; point++ {
		if point < 0xd800 || point > 0xdfff {
			input := string(point)
			if output := Lower(input); input != output {
				_, _ = fmt.Fprintf(lower, "%x:%s\n", point, output)
			}
			if output := Title(input); input != output {
				_, _ = fmt.Fprintf(title, "%x:%s\n", point, output)
			}
		}
		var flag byte
		if isCaseIgnorable(point) {
			flag = 1
		}
		_, _ = ignorable.Write([]byte{flag})
	}
	if fmt.Sprintf("%x", lower.Sum(nil)) != "41cbc053e29ec86177d97c065af93df94e1180f3f6823b5bf9f4c25d9c77ab64" ||
		fmt.Sprintf("%x", title.Sum(nil)) != "5c5313a924cb7c81d8cd80a030aed468af69dc7a9aff432df03d371dbd1ad9f6" ||
		fmt.Sprintf("%x", ignorable.Sum(nil)) != "cfafce96d60bf7a7d32406fc211aaf08a204804a8db4f456f16759e0c417bf0f" {
		t.Fatal("pinned Python3.12.14 Unicode15 casing/property drift")
	}
}

func TestContextualCasingSourceObservations(t *testing.T) {
	// Independently observed Python3.12.14 outputs, including overlapping cased
	// and case-ignorable properties and dots that continue final-sigma context.
	for _, sample := range [][3]string{
		{"", "", ""},
		{"İSTANBUL", "i̇stanbul", "İstanbul"},
		{"ΟΣ", "ος", "Ος"},
		{"ΟΣ.example", "οσ.example", "Οσ.Example"},
		{"ﬃ Æ-ǄǄ ᾇ", "ﬃ æ-ǆǆ ᾇ", "Ffi Æ-ǅǆ ᾏ"},
		{"AΣ", "aς", "Aς"},
		{"AΣ.A", "aσ.a", "Aσ.A"},
		{"AΣ-", "aς-", "Aς-"},
		{"AΣͅA", "aσͅa", "Aσͅa"},
		{"AͅΣ", "aͅς", "Aͅς"},
		{"-ͅΣ", "-ͅσ", "-Ισ"},
		{"áΣ", "áς", "ÁΣ"},
		{"áΣ́", "áς́", "ÁΣ́"},
		{"AΣ́", "aς́", "Aς́"},
		{"AΣ́A", "aσ́a", "Aσ́A"},
		{"A’Σ", "a’ς", "A’Σ"},
		{"aᲉΣ", "aᲉσ", "AᲉΣ"},
		{"ßs", "ßs", "Sss"},
		{"ᾷᾧ", "ᾷᾧ", "ᾼ͂ᾧ"},
		{"ͅAͅΣ", "ͅaͅς", "Ιaͅς"},
		{"ΣΣΣ", "σσς", "Σσς"},
		{"i̇stanbul", "i̇stanbul", "İStanbul"},
		{"straße_it’s-careers", "straße_it’s-careers", "Straße_It’S-Careers"},
	} {
		if Lower(sample[0]) != sample[1] || Title(sample[0]) != sample[2] {
			t.Fatalf("%q: lower=%q title=%q", sample[0], Lower(sample[0]), Title(sample[0]))
		}
	}
	for _, ranges := range [][]caseRange{lowerRanges[:], titleRanges[:]} {
		last := rune(-1)
		for _, entry := range ranges {
			if entry.first <= last || entry.first > entry.last || entry.step < 1 || entry.step > 2 {
				t.Fatal("case range order/bounds")
			}
			last = entry.last
		}
	}
}
