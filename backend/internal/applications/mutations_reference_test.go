package applications

import (
	"bytes"
	"io"
)

type ledgerOutput struct{ bytes.Buffer }

func (b *ledgerOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}
