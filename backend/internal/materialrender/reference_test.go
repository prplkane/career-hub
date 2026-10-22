package materialrender

import (
	"fmt"
	"reflect"
	"sort"
)

func difference(a, b any, path string) string {
	if reflect.DeepEqual(a, b) {
		return ""
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			break
		}
		keys := []string{}
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if diff := difference(x[k], y[k], path+"/"+k); diff != "" {
				return diff
			}
		}
		if len(x) != len(y) {
			return path + ": object keys differ"
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			break
		}
		for i := 0; i < len(x) && i < len(y); i++ {
			if diff := difference(x[i], y[i], fmt.Sprintf("%s/%d", path, i)); diff != "" {
				return diff
			}
		}
		if len(x) != len(y) {
			return fmt.Sprintf("%s: array lengths %d / %d", path, len(x), len(y))
		}
	}
	return fmt.Sprintf("%s: expected %.180v; got %.180v", path, a, b)
}
