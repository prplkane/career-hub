package materialintegrity

import (
	"bytes"
)

type limitedFixtureOutput struct{ bytes.Buffer }

func (b *limitedFixtureOutput) Write(raw []byte) (int, error) {
	if b.Len()+len(raw) > 2<<20 {
		return 0, failure("MATERIAL_FIXTURE_LIMIT")
	}
	return b.Buffer.Write(raw)
}
