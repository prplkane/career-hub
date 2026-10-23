package envelope

import (
	"bytes"
	"errors"
	"testing"
)

// FuzzEnvelope bounds each generated input, keeping CI runs inexpensive. It
// checks malformed framing and authenticated-data handling without filesystem
// access. Large-object boundaries are covered by deterministic unit tests.
func FuzzEnvelope(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("CHDOC\x00"))
	f.Add(goldenVector(f))
	f.Add(rawEnvelope([]byte(canonicalBlob)))
	f.Add(rawEnvelope([]byte(`{"purpose":[]}`)))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64*1024 {
			t.Skip()
		}
		before := bytes.Clone(data)
		header, inspectErr := Inspect(data)
		decrypted, decryptErr := Decrypt(data, testKey(), blobContext())
		if !bytes.Equal(before, data) {
			t.Fatal("codec mutated fuzz input")
		}
		if inspectErr != nil {
			assertCode(t, inspectErr, CodeInvalid)
			assertCode(t, decryptErr, CodeInvalid)
			if header != (Header{}) {
				t.Fatal("failed inspection returned a header")
			}
		}
		if decryptErr != nil {
			var typed *Error
			if !errors.As(decryptErr, &typed) {
				t.Fatalf("unsanitized decryption error: %T", decryptErr)
			}
			if decrypted.Plaintext != nil || decrypted.Header != (Header{}) {
				t.Fatal("failed decryption exposed data")
			}
			return
		}
		if inspectErr != nil || decrypted.Header != header {
			t.Fatal("decryption bypassed inspection")
		}
		resealed := encrypted(t, decrypted.Plaintext, blobContext())
		roundTrip, err := Decrypt(resealed, testKey(), blobContext())
		if err != nil || !bytes.Equal(roundTrip.Plaintext, decrypted.Plaintext) {
			t.Fatalf("successful fuzz input failed round trip: %v", err)
		}
	})
}
