package protocol

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// The desktop host's window token (ADR SEC-250) must have exactly the format
// the HTTP handler generates for itself: 32 random bytes, unpadded URL-safe
// base64, spelled canonically. Anything else refuses startup.
func TestStartSessionToken(t *testing.T) {
	good := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xA5}, 32))

	// Same 32 bytes, different spelling: the last character carries 2 unused
	// bits, and a lenient decoder ignores them. Canonical spelling has them 0.
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, good[len(good)-1])
	nonCanonical := good[:len(good)-1] + string(alphabet[last|1])

	cases := []struct {
		name  string
		value any
		ok    bool
	}{
		{"canonical 32 bytes", good, true},
		{"non-canonical spelling", nonCanonical, false},
		{"standard alphabet", strings.NewReplacer("-", "+", "_", "/").Replace(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xFB}, 32))), false},
		{"padded", base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), false},
		{"too short", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16)), false},
		{"too long", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 48)), false},
		{"surrounding space", " " + good, false},
		{"empty", "", false},
		{"number", 7, false},
		{"null", nil, false},
		{"bool", true, false},
		{"array", []any{good}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := startPayload()
			p["session_token"] = tc.value
			result, err := ParseStart(p, PathOptions{Style: Windows})
			if tc.ok {
				requireCode(t, err, "")
				if result.SessionToken != tc.value {
					t.Fatal("accepted token was not returned unchanged")
				}
				return
			}
			requireCode(t, err, "SIDECAR_START_INVALID_TOKEN")
			if s, isString := tc.value.(string); isString && s != "" && strings.Contains(err.Error(), s) {
				t.Fatal("an error message disclosed the token")
			}
		})
	}

	// Omitting the field keeps the version 1 record valid; the handler then
	// generates its own token.
	result, err := ParseStart(startPayload(), PathOptions{Style: Windows})
	requireCode(t, err, "")
	if result.SessionToken != "" {
		t.Fatal("a token appeared from nowhere")
	}

	// The token is never echoed back in a record the host reads.
	if ready := ReadyRecord("1.2.3", 49152, "dev"); ready["session_token"] != nil {
		t.Fatal("the ready record must not carry the token")
	}
}
