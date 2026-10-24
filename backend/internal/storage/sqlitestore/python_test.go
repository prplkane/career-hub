package sqlitestore

import (
	"bytes"
	"fmt"
)

type limitedOutput struct {
	data     bytes.Buffer
	overflow bool
}

func (b *limitedOutput) Write(raw []byte) (int, error) {
	if len(raw) > 8192-b.data.Len() {
		b.overflow = true
		return 0, fmt.Errorf("fixture output limit")
	}
	return b.data.Write(raw)
}
