package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestMutationWitnessPreservesSQLTypes(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range []any{nil, "", []byte{}, int64(0), float64(0), int64(1), float64(1), "1", []byte("1"), string([]byte{0xff}), []byte{0xff}} {
		h := sha256.New()
		remaining := MaxReadBytes
		if !hashSQLCell(h, v, &remaining) {
			t.Fatal("supported SQL value refused")
		}
		key := hex.EncodeToString(h.Sum(nil))
		if seen[key] {
			t.Fatal("typed SQL values collapsed")
		}
		seen[key] = true
	}
	for _, v := range []any{true, 0, make([]byte, MaxCellBytes+1)} {
		remaining := MaxReadBytes
		if hashSQLCell(sha256.New(), v, &remaining) {
			t.Fatal("unsupported SQL cell accepted")
		}
	}
	remaining := 8
	if hashSQLCell(sha256.New(), nil, &remaining) {
		t.Fatal("read budget not enforced")
	}
	if _, e := MutationWitnesses(context.Background(), nil, nil); e == nil {
		t.Fatal("nil transaction")
	}
	if e := CheckPublicationReferences(nil, nil, nil); e == nil {
		t.Fatal("nil reference reader")
	}
	if e := CheckPublicationContent(nil, nil, nil); e == nil {
		t.Fatal("nil content reader")
	}
}
