package agentmaterials

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(raw []byte) (int, error) {
	if len(raw) > (32<<20)-b.Len() {
		return 0, fmt.Errorf("oracle output limit")
	}
	return b.Buffer.Write(raw)
}

func decode(t *testing.T, raw []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var x any
	if e := d.Decode(&x); e != nil {
		t.Fatal(e)
	}
	return x
}
