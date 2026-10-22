package main

import (
	"bytes"
	"os"
	"testing"
)

func TestReproductionAndPinnedInputs(t *testing.T) {
	raw, err := os.ReadFile("../../ucd/CaseFolding-15.0.0.txt")
	if err != nil {
		t.Fatal(err)
	}
	license, err := os.ReadFile("../../ucd/LICENSE.txt")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := generate(raw, license)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("../../tables_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("generated table is stale or manually changed")
	}
	for _, pair := range [][2][]byte{{append([]byte("changed"), raw...), license}, {raw, append([]byte("changed"), license...)}} {
		if _, err := generate(pair[0], pair[1]); err == nil {
			t.Fatal("modified provenance accepted")
		}
	}
}
