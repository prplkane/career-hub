package applicationtexts

import (
	"bytes"
	"io"
)

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > 2<<20-b.Len() {
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}
