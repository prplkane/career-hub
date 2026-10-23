package vaultwrite

import (
	"bytes"
	"fmt"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type referenceStep struct {
	Target  string         `json:"target"`
	ID      string         `json:"id"`
	Payload map[string]any `json:"payload"`
	Stale   bool           `json:"stale"`
}

type boundedOutput struct{ buffer bytes.Buffer }

func (b *boundedOutput) Len() int      { return b.buffer.Len() }
func (b *boundedOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > (8<<20)-b.Len() {
		return 0, fmt.Errorf("reference output limit")
	}
	return b.buffer.Write(p)
}

type memoryReader map[string][]byte

func (r memoryReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if limit != cataloginput.YAMLMaxInputBytes {
		panic("read bound")
	}
	raw, present := r[path]
	return raw, present, nil
}
