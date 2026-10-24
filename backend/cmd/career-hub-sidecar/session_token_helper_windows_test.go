//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
)

// testSessionToken stands in for the window token the desktop host sends in
// the start record (ADR SEC-250); no route returns it, so tests supply it.
// Computed rather than written out so no secret-shaped literal is committed.
var testSessionToken = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
