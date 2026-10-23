package vaultread

import (
	"context"
	"testing"
)

func TestClosedViewSelectorRefusesBeforeRootAccess(t *testing.T) {
	for value := 0; value < 256; value++ {
		view := View(value)
		if view.Valid() {
			if view < ProfileView || view > SetupView {
				t.Fatal("unexpected admitted view")
			}
			// Cancellation must propagate through every selected read service.
			if payload, failure := Read(nil, "", view); payload != nil || failure == nil || failure.Code != "VAULT_READ_CANCELED" {
				t.Fatal("known view did not reach a cancelable read service")
			}
			continue
		}
		for _, ctx := range []context.Context{nil, context.Background()} {
			payload, failure := Read(ctx, "", view)
			if payload != nil || failure == nil || failure.Code != "VAULT_VIEW_INVALID" || failure.Stage != "view" {
				t.Fatal("unknown view accessed a root or returned a projection")
			}
		}
	}
}
