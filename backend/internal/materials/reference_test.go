package materials

import (
	"bytes"
	"fmt"
)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(raw []byte) (int, error) {
	if len(raw) > (16<<20)-b.Len() {
		return 0, fmt.Errorf("synthetic oracle output limit")
	}
	return b.Buffer.Write(raw)
}
