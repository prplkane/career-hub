package jobevaluation

import (
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
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
	for _, item := range value.Items {
		out.Items = append(out.Items, item.decoded(t))
	}
	for _, entry := range value.Entries {
		if len(entry) != 2 {
			t.Fatal("malformed reference value")
		}
		out.Entries = append(out.Entries, cataloginput.Entry{Key: entry[0].decoded(t), Value: entry[1].decoded(t)})
	}
	return out
}
