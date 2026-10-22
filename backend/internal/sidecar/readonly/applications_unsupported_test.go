//go:build !windows

package readonly

import (
	"context"
	"reflect"
	"testing"
)

func TestApplicationSummariesUnsupportedStorageHasNoFallback(t *testing.T) {
	base, start := fixture(t)
	w, err := Open(start)
	if err != nil {
		t.Fatal(err)
	}
	before := tree(t, base)
	payload, err := w.ApplicationSummaries(context.Background(), []string{"one"})
	requireCode(t, err, "APPLICATION_STORAGE_UNSUPPORTED")
	if payload != nil || !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("unsupported storage returned data or modified the fixture")
	}
}
