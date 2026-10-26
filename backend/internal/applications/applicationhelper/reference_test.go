package applicationhelper

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type oracleStep struct {
	Operation string         `json:"operation"`
	Fields    map[string]any `json:"fields"`
	Stale     bool           `json:"stale"`
}
type oracleOutput struct {
	Response json.RawMessage
	Error    *string
	Files    map[string]*string
}
type oracleCase struct {
	Name       string             `json:"name"`
	Files      map[string]*string `json:"files"`
	Steps      []oracleStep       `json:"steps"`
	Source     []oracleOutput     `json:"source"`
	Correction string             `json:"correction,omitempty"`
	Resumes    []map[string]any   `json:"resumes,omitempty"`
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(raw []byte) (int, error) {
	if len(raw) > (16<<20)-b.Len() {
		return 0, fmt.Errorf("synthetic tool output limit")
	}
	return b.Buffer.Write(raw)
}
