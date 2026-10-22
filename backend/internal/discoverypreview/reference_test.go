package discoverypreview

import (
	"bytes"
	"errors"
)

type referenceOutput struct{ bytes.Buffer }

func (out *referenceOutput) Write(raw []byte) (int, error) {
	if out.Len()+len(raw) > 4<<20 {
		return 0, errors.New("reference output limit")
	}
	return out.Buffer.Write(raw)
}
