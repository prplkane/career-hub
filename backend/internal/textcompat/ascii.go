package textcompat

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ASCIIFromNFKD15 matches Python3.12 NFKD followed by ASCII encoding with
// errors="ignore". Canonical reordering changes only non-ASCII combining marks,
// which are discarded, so per-scalar decomposition preserves the ASCII
// projection without stream-safe insertions. Later Unicode assignments stay
// opaque, as in the pinned reference. Case folding is intentionally separate:
// folding before ASCII filtering would incorrectly retain sharp-s as "ss".
func ASCIIFromNFKD15(value string) (string, error) {
	if len(value) > MaxNormalizationBytes || !utf8.ValidString(value) || norm.Version != "17.0.0" {
		return "", ErrNormalizationInput
	}
	var out strings.Builder
	out.Grow(min(len(value), MaxNormalizationBytes))
	for _, point := range value {
		decomposition := string(point)
		if point >= 0x80 && analysisFlags(point)&1 != 0 {
			decomposition = norm.NFKD.String(decomposition)
		}
		for _, part := range decomposition {
			if part < 0x80 {
				if out.Len() == MaxNormalizationBytes {
					return "", ErrNormalizationInput
				}
				out.WriteByte(byte(part))
			}
		}
	}
	return out.String(), nil
}
