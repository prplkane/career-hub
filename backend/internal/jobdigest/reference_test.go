package jobdigest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
)

type boundedOutput struct{ bytes.Buffer }

func (out *boundedOutput) Write(value []byte) (int, error) {
	if out.Len()+len(value) > 32<<20 {
		return 0, errors.New("oracle output limit")
	}
	return out.Buffer.Write(value)
}

func difference(want, got any, path string) string {
	switch expected := want.(type) {
	case json.Number:
		actual, ok := got.(json.Number)
		if !ok {
			return path + ": numeric type differs"
		}
		a, okA := new(big.Rat).SetString(expected.String())
		b, okB := new(big.Rat).SetString(actual.String())
		if !okA || !okB || a.Cmp(b) != 0 {
			return fmt.Sprintf("%s: number want %s got %s", path, expected, actual)
		}
	case map[string]any:
		actual, ok := got.(map[string]any)
		if !ok || len(expected) != len(actual) {
			return path + ": mapping shape differs"
		}
		for key, value := range expected {
			other, present := actual[key]
			if !present {
				return path + "." + key + ": missing"
			}
			if diff := difference(value, other, path+"."+key); diff != "" {
				return diff
			}
		}
	case []any:
		actual, ok := got.([]any)
		if !ok || len(expected) != len(actual) {
			return fmt.Sprintf("%s: list length/type differs; want %.1500v got %.1500v", path, expected, got)
		}
		for index, value := range expected {
			if diff := difference(value, actual[index], fmt.Sprintf("%s[%d]", path, index)); diff != "" {
				return diff
			}
		}
	default:
		if !reflect.DeepEqual(want, got) {
			return fmt.Sprintf("%s: want %.1500v got %.1500v", path, want, got)
		}
	}
	return ""
}
