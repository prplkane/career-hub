package materialwrite

import (
	"bytes"
	"strings"
	"testing"
)

func TestNativeExportReceiptCanonicalAndPathAdmission(t *testing.T) {
	// No filesystem is needed to reject caller paths, flags or invalid selectors.
	s := New("not a data home")
	for _, r := range []ExportRequest{{"../job", "resume", "txt"}, {"job", "unknown", "txt"}, {"job", "resume", "html"}, {"job", "resume", strings.Repeat("a", 300)}} {
		out, e := s.Export(nil, "resources", r)
		if e == nil || !noResult(out) || out.Committed {
			t.Fatal("invalid export request entered storage")
		}
	}
	for _, id := range []string{"../manifest.json", "material_" + strings.Repeat("A", 64), "material_" + strings.Repeat("0", 64) + "/extra"} {
		out, e := s.ReadExport(nil, "resources", ExportReadRequest{"job", "resume", id})
		if e == nil || !noResult(out) {
			t.Fatal("caller artifact path accepted")
		}
	}
	id := "material_" + strings.Repeat("a", 64)
	valid := receiptBytes(id, []byte("fixture"))
	for _, raw := range [][]byte{nil, append([]byte(" "), valid...), bytes.Replace(valid, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1), bytes.Replace(valid, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1), bytes.Replace(valid, []byte(`"artifact_id"`), []byte(`"Artifact_ID"`), 1), []byte(strings.Repeat(" ", maxExportReceipt+1))} {
		if _, e := decodeReceipt(raw, id); e == nil {
			t.Fatal("noncanonical receipt admitted")
		}
	}
}
