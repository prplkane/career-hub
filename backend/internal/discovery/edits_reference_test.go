package discovery

import (
	"bytes"
	"io"
)

type writeOutput struct{ bytes.Buffer }

func (b *writeOutput) Write(raw []byte) (int, error) {
	if b.Len()+len(raw) > 8<<20 {
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(raw)
}
