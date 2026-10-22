package catalogwrite

import (
	"bytes"
	"errors"
)

type groupEncodingReferenceOutput struct{ buffer bytes.Buffer }

func (output *groupEncodingReferenceOutput) Write(p []byte) (int, error) {
	if len(p) > (64<<10)-output.buffer.Len() {
		return 0, errors.New("synthetic group reference output limit")
	}
	return output.buffer.Write(p)
}
