package jobcard

import (
	"context"
	"encoding/base64"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

type referenceValue struct {
	Kind, Text string
	Boolean    bool `json:"value"`
	Items      []referenceValue
	Entries    [][]referenceValue
}

func (value referenceValue) decoded(t *testing.T) cataloginput.Value {
	t.Helper()
	out := cataloginput.Value{Kind: value.Kind, Text: value.Text, Boolean: value.Boolean}
	if value.Kind == "bytes" {
		var err error
		out.Bytes, err = base64.StdEncoding.DecodeString(value.Text)
		out.Text = ""
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range value.Items {
		out.Items = append(out.Items, item.decoded(t))
	}
	for _, entry := range value.Entries {
		if len(entry) != 2 {
			t.Fatal("bad reference entry")
		}
		out.Entries = append(out.Entries, cataloginput.Entry{Key: entry[0].decoded(t), Value: entry[1].decoded(t)})
	}
	return out
}

func syntheticCheck(ctx context.Context, path string) (bool, *Error) {
	return path == "jobs/incoming/synthetic.txt", nil
}
