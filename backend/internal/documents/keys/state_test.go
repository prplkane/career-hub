package keys

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	testHome       = "dh_0123456789abcdef0123456789abcdef"
	testKey        = "key_11111111111111111111111111111111"
	canonicalState = `{"active_key_id":"key_11111111111111111111111111111111","algorithm":"AES-256-GCM","data_home_id":"dh_0123456789abcdef0123456789abcdef","version":1}` + "\n"
)

func validState() State {
	return State{Version: Version, Algorithm: Algorithm, DataHomeID: testHome, ActiveKeyID: testKey}
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("expected error code %s, got %v", code, err)
	}
}

func TestStateCanonicalRoundTripAndOwnership(t *testing.T) {
	state := validState()
	data, err := state.CanonicalBytes()
	if err != nil || string(data) != canonicalState {
		t.Fatal("canonical bytes do not match the Python format")
	}
	parsed, err := ParseState(data)
	if err != nil || parsed != state {
		t.Fatal("canonical state round trip failed")
	}
	data[0] = '!'
	fresh, err := parsed.CanonicalBytes()
	if err != nil || string(fresh) != canonicalState {
		t.Fatal("caller mutation changed state")
	}
}

func TestCanonicalBytesSerializesInvalidTypedStateWithPythonEscaping(t *testing.T) {
	// Characterized with actual src.document_keys.DocumentKeyState.canonical_bytes.
	state := State{Version: -7, ActiveKeyID: "\"\\/", Algorithm: "<&>\x7f\u0080😀", DataHomeID: "\b\f\n\r\t\x00\x1f"}
	want := `{"active_key_id":"\"\\/","algorithm":"<&>\u007f\u0080\ud83d\ude00","data_home_id":"\b\f\n\r\t\u0000\u001f","version":-7}` + "\n"
	got, err := state.CanonicalBytes()
	if err != nil || string(got) != want {
		t.Fatal("typed invalid-state serialization differs from Python")
	}
	_, err = ParseState(got)
	requireCode(t, err, CodeStateInvalid)
	for _, state := range []State{
		{ActiveKeyID: string([]byte{0xff})},
		{Algorithm: string([]byte{0xff})},
		{DataHomeID: string([]byte{0xff})},
	} {
		data, err := state.CanonicalBytes()
		requireCode(t, err, CodeStateInvalid)
		if data != nil {
			t.Fatal("invalid scalar UTF-8 returned bytes")
		}
	}
}

func TestParseStateRejectsNoncanonicalAndMalformedBytes(t *testing.T) {
	cases := map[string][]byte{
		"nil": nil, "empty": {}, "null": []byte("null\n"), "array": []byte("[]\n"),
		"boolean": []byte("true\n"), "number": []byte("1\n"), "string": []byte(`"state"`),
		"missing": []byte("{}\n"), "malformed": []byte("{\n"),
		"wrong field type": []byte(strings.Replace(canonicalState, `"AES-256-GCM"`, `[]`, 1)),
		"null field":       []byte(strings.Replace(canonicalState, `"AES-256-GCM"`, `null`, 1)),
		"missing field":    []byte(strings.Replace(canonicalState, `"version":1`, `"another":1`, 1)),
		"unknown":          []byte(strings.Replace(canonicalState, `"version":1`, `"extra":1,"version":1`, 1)),
		"duplicate":        []byte(strings.Replace(canonicalState, `"version":1`, `"version":1,"version":1`, 1)),
		"wrong case":       []byte(strings.Replace(canonicalState, `"version"`, `"Version"`, 1)),
		"bool version":     []byte(strings.Replace(canonicalState, `"version":1`, `"version":true`, 1)),
		"float version":    []byte(strings.Replace(canonicalState, `"version":1`, `"version":1.0`, 1)),
		"exponent version": []byte(strings.Replace(canonicalState, `"version":1`, `"version":1e0`, 1)),
		"null version":     []byte(strings.Replace(canonicalState, `"version":1`, `"version":null`, 1)),
		"large version":    []byte(strings.Replace(canonicalState, `"version":1`, `"version":9999999999999999999999999999`, 1)),
		"missing LF":       []byte(strings.TrimSuffix(canonicalState, "\n")),
		"extra LF":         []byte(canonicalState + "\n"), "CRLF": []byte(strings.ReplaceAll(canonicalState, "\n", "\r\n")),
		"space": []byte(" " + canonicalState), "second value": []byte(canonicalState + "{}"),
		"reordered":      []byte(`{"version":1,"active_key_id":"` + testKey + `","algorithm":"AES-256-GCM","data_home_id":"` + testHome + `"}` + "\n"),
		"escaped key":    []byte(strings.Replace(canonicalState, `"version"`, `"\u0076ersion"`, 1)),
		"escaped value":  []byte(strings.Replace(canonicalState, `"AES-256-GCM"`, `"\u0041ES-256-GCM"`, 1)),
		"lone surrogate": []byte(strings.Replace(canonicalState, `"AES-256-GCM"`, `"\ud800"`, 1)),
		"invalid UTF8":   append([]byte{0xff}, []byte(canonicalState)...),
		"bound":          bytes.Repeat([]byte{' '}, MaxStateBytes),
		"over bound":     bytes.Repeat([]byte{' '}, MaxStateBytes+1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			before := bytes.Clone(input)
			state, err := ParseState(input)
			requireCode(t, err, CodeStateInvalid)
			if state != (State{}) || !bytes.Equal(input, before) {
				t.Fatal("failure returned state or mutated input")
			}
		})
	}
}

func TestTypedInvalidStates(t *testing.T) {
	for name, change := range map[string]func(*State){
		"version":        func(s *State) { s.Version = 2 },
		"algorithm":      func(s *State) { s.Algorithm = "aes-256-gcm" },
		"home short":     func(s *State) { s.DataHomeID = "dh_0" },
		"home prefix":    func(s *State) { s.DataHomeID = "zz_0123456789abcdef0123456789abcdef" },
		"home uppercase": func(s *State) { s.DataHomeID = strings.ToUpper(testHome) },
		"home nonhex":    func(s *State) { s.DataHomeID = "dh_g123456789abcdef0123456789abcdef" },
		"home newline":   func(s *State) { s.DataHomeID += "\n" },
		"key short":      func(s *State) { s.ActiveKeyID = "key_0" },
		"key nonhex":     func(s *State) { s.ActiveKeyID = "key_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" },
		"key prefix":     func(s *State) { s.ActiveKeyID = "bad_11111111111111111111111111111111" },
	} {
		t.Run(name, func(t *testing.T) {
			state := validState()
			change(&state)
			data, err := state.CanonicalBytes()
			if err != nil {
				t.Fatal("serializer incorrectly performed semantic validation")
			}
			_, err = ParseState(data)
			requireCode(t, err, CodeStateInvalid)
			backend := &memoryBackend{values: map[string][]byte{}}
			manager := newTestManager(t, "stable", backend, nil)
			loaded, err := manager.Load(state)
			requireCode(t, err, CodeStateInvalid)
			if loaded.Key != nil || loaded.State != (State{}) {
				t.Fatal("invalid load returned a result")
			}
			requireCode(t, manager.DiscardUnpublished(state), CodeStateInvalid)
			if len(backend.trace) != 0 {
				t.Fatal("invalid state reached backend")
			}
		})
	}
}

func TestErrorMessagesAreFixedAndSourceCompatible(t *testing.T) {
	for code, want := range map[string]string{
		CodeStateInvalid:     "Document key state is invalid.",
		CodeUnavailable:      "The document encryption key is unavailable. Restore access using the original account and device.",
		CodeKeyInvalid:       "The stored document encryption key is invalid.",
		CodeGenerationFailed: "Secure random key generation failed.",
		CodeInvalidChannel:   "Unsupported Career Hub release channel.",
		CodeBackendRequired:  "An explicit document credential backend is required.",
	} {
		err := &Error{Code: code}
		if err.Error() != want || fmt.Sprintf("%#v", err) != want || fmt.Sprintf("%+v", err) != want {
			t.Fatal("error formatting mismatch")
		}
	}
	var absent *Error
	if absent.Error() != "Document key state is invalid." || (&Error{Code: "unknown"}).Error() != absent.Error() {
		t.Fatal("unsafe default error")
	}
}

func FuzzParseState(f *testing.F) {
	f.Add([]byte(canonicalState))
	f.Add([]byte("null\n"))
	f.Add([]byte("{}\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 8192 {
			t.Skip()
		}
		state, err := ParseState(input)
		if err != nil {
			requireCode(t, err, CodeStateInvalid)
			return
		}
		canonical, err := state.CanonicalBytes()
		if err != nil || !bytes.Equal(canonical, input) {
			t.Fatal("accepted noncanonical bytes")
		}
	})
}
