package privacy

import (
	"bytes"
	"errors"
)

type referenceOutput struct{ bytes bytes.Buffer }

func (out *referenceOutput) Write(p []byte) (int, error) {
	if out.bytes.Len()+len(p) > 16<<10 {
		return 0, errors.New("synthetic reference output limit")
	}
	return out.bytes.Write(p)
}
