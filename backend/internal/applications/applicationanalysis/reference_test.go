package applicationanalysis

import (
	"bytes"
	"errors"
)

type boundedOutput struct{ bytes.Buffer }

func (out *boundedOutput) Write(value []byte) (int, error) {
	if out.Len()+len(value) > 32<<20 {
		return 0, errors.New("oracle output budget exceeded")
	}
	return out.Buffer.Write(value)
}
