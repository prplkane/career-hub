//go:build windows

package httpapi

import (
	"bytes"
	"errors"
)

type queryBoundedOutput struct {
	data  bytes.Buffer
	limit int
}

func (w *queryBoundedOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.data.Len() {
		return 0, errors.New("query reference output limit")
	}
	return w.data.Write(p)
}
