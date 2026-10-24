package vaultread

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/skills/cataloginput"
)

// MaxProfileBytes matches the existing sidecar response ceiling. This bounds
// retained output, not total live memory; one string's standard-library JSON
// encoding can temporarily expand up to six times its admitted UTF-8 byte size.
const MaxProfileBytes = 4 << 20

// ReadProfile reads the eight fixed files, validates their manual/reference
// contract and returns the unnormalized profile with a same-read SHA-256 revision.
// It neither inserts defaults nor creates missing directories/files. The caller
// must admit the root and own cross-file writer coordination/identity checks.
func ReadProfile(ctx context.Context, root string) (json.RawMessage, *Error) {
	state, failure := openVault(ctx, root)
	if failure != nil {
		return nil, failure
	}
	return profilePayload(ctx, state)
}

func profilePayload(ctx context.Context, state *vaultState) (json.RawMessage, *Error) {
	if failure := validateProfileState(ctx, state); failure != nil {
		return nil, failure
	}
	encoder := profileEncoder{ctx: ctx, source: "profile"}
	encoder.recordProjection(state, profileIndex)
	return encoder.result()
}

type profileEncoder struct {
	ctx     context.Context
	source  string
	output  []byte
	failure *Error
}

func (e *profileEncoder) fail(code string) {
	if e.failure == nil {
		e.failure = &Error{Stage: "response", Source: e.source, Code: code}
	}
}

func (e *profileEncoder) add(raw []byte) {
	if e.failure != nil {
		return
	}
	if e.ctx == nil || e.ctx.Err() != nil {
		e.failure = canceled()
		return
	}
	if len(raw) > MaxProfileBytes-len(e.output) {
		e.fail("VAULT_RESPONSE_LIMIT")
		return
	}
	e.output = append(e.output, raw...)
}

func (e *profileEncoder) scalar(value string) {
	if e.failure != nil {
		return
	}
	if len(value) > MaxProfileBytes-len(e.output) {
		e.fail("VAULT_RESPONSE_LIMIT")
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		e.fail("VAULT_RESPONSE_UNSUPPORTED")
		return
	}
	e.add(raw)
}

// Python json.dumps accepts scalar dict keys and can emit duplicate JSON names
// after conversion (e.g. null and "null"). Preserve post-dict insertion order and
// these names in the wire object; don't silently drop an entry via a Go map.
func keyText(key cataloginput.Value) (string, bool) {
	switch key.Kind {
	case "string", "int":
		return key.Text, true
	case "float":
		return key.Text, json.Valid([]byte(key.Text))
	case "bool":
		if key.Boolean {
			return "true", true
		}
		return "false", true
	case "null":
		return "null", true
	}
	return "", false
}

func (e *profileEncoder) value(value cataloginput.Value) {
	if e.failure != nil {
		return
	}
	switch value.Kind {
	case "string":
		e.scalar(value.Text)
	case "int", "float":
		// Input Text is canonical from ParseYAML. NaN/Infinity are not valid
		// browser JSON; refuse explicitly instead of emitting null or strings.
		if !json.Valid([]byte(value.Text)) {
			e.fail("VAULT_RESPONSE_UNSUPPORTED")
			return
		}
		e.add([]byte(value.Text))
	case "bool":
		if value.Boolean {
			e.add([]byte("true"))
		} else {
			e.add([]byte("false"))
		}
	case "null":
		e.add([]byte("null"))
	case "sequence":
		e.add([]byte("["))
		for index, item := range value.Items {
			if e.failure != nil {
				return
			}
			if index != 0 {
				e.add([]byte(","))
			}
			e.value(item)
		}
		e.add([]byte("]"))
	case "mapping":
		e.add([]byte("{"))
		for index, entry := range value.Entries {
			if e.failure != nil {
				return
			}
			key, ok := keyText(entry.Key)
			if !ok {
				e.fail("VAULT_RESPONSE_UNSUPPORTED")
				return
			}
			if index != 0 {
				e.add([]byte(","))
			}
			e.scalar(key)
			e.add([]byte(":"))
			e.value(entry.Value)
		}
		e.add([]byte("}"))
	default:
		// Dates/datetimes/bytes may pass manual Vault validation but fail the
		// source JSON boundary. Never stringify or discard unknown fields.
		e.fail("VAULT_RESPONSE_UNSUPPORTED")
	}
}
