package main

import (
	"context"
	"testing"

	"careerhub.local/backend/internal/sidecar/protocol"
)

func TestOnlyInternalSidecarFlagAccepted(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"--sidecar", "--data-root", "private-marker"}, {"--sidecar-bootstrap", "--sidecar"}, {"--sidecar-bootstrap", "--extension-origin"}, {"--sidecar-bootstrap", "--extension-origin", "https://example.test"}, {"--sidecar", "--extension-origin", "chrome-extension://fake"}, {"--desktop-maintenance", "--extension-origin", "chrome-extension://fake"}} {
		if execute(args, nil, nil) != 2 {
			t.Fatal("unsupported CLI started a runtime")
		}
	}
	if execute([]string{"--desktop-maintenance"}, nil, nil) != 6 {
		t.Fatal("maintenance mode was not routed to its owned process adapter")
	}
}

func TestCanceledPreparationDoesNotReadPaths(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepare(ctx, protocol.StartRecord{}); err == nil {
		t.Fatal("canceled startup reached workspace preparation")
	}
	if _, err := prepareBootstrap(ctx, protocol.StartRecord{}); err == nil {
		t.Fatal("canceled bootstrap reached workspace preparation")
	}
}

func TestExtensionPortRequiresExplicitOriginsAndCanonicalFixedValue(t *testing.T) {
	for _, tail := range [][]string{{"--extension-port", "8766"}, {"--extension-origin", "chrome-extension://test", "--extension-port", "0"}, {"--extension-origin", "chrome-extension://test", "--extension-port", "8767"}, {"--extension-origin", "chrome-extension://test", "--extension-port", "8766", "--extension-port", "8766"}} {
		if execute(append([]string{"--sidecar-bootstrap"}, tail...), nil, nil) != 2 {
			t.Fatal("invalid extension listener arguments reached startup")
		}
	}
	if execute([]string{"--sidecar-bootstrap", "--extension-origin", "chrome-extension://test", "--extension-port", "8766"}, nil, nil) != 3 {
		t.Fatal("explicit extension listener did not reach owned runtime")
	}
}
