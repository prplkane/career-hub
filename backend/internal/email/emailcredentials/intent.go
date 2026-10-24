package emailcredentials

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"careerhub.local/backend/internal/storage/datahome"
)

// MaxIntentBytes bounds the entire nonsecret recovery file, not just its JSON
// payload. The file is never a place for passwords, accounts or mail content.
const MaxIntentBytes = 2048

const (
	InitialConfigure = "configure"
	Rotate           = "rotate"
	PromoteLegacy    = "promote_legacy"
	Disconnect       = "disconnect"
	Forget           = "forget"
	MaxRevision      = 1_000_000_000_000
)

var (
	ErrIntentInvalid     = errors.New("EMAIL_CREDENTIAL_INTENT_INVALID")
	ErrIntentHomeChanged = errors.New("EMAIL_CREDENTIAL_INTENT_HOME_CHANGED")
)

// RowWitness contains only the bounded database facts needed to distinguish
// pre-commit from post-commit state. It deliberately excludes account identity.
type RowWitness struct {
	Present    bool   `json:"present"`
	Revision   int64  `json:"revision"`
	Enabled    bool   `json:"enabled"`
	SecretName string `json:"secret_name"`
}

type Intent struct {
	Version   int           `json:"version"`
	Home      datahome.Info `json:"home"`
	Operation string        `json:"operation"`
	Before    RowWitness    `json:"before"`
	After     RowWitness    `json:"after"`
}

type intentEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}

// EncodeIntent produces a canonical checksummed recovery record. It does not
// publish the record or perform any credential/database operation.
func EncodeIntent(intent Intent) ([]byte, error) {
	if !validIntent(intent) {
		return nil, ErrIntentInvalid
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return nil, ErrIntentInvalid
	}
	raw, err := json.Marshal(intentEnvelope{Payload: payload, SHA256: digest(payload)})
	if err != nil || len(raw) > MaxIntentBytes {
		return nil, ErrIntentInvalid
	}
	return raw, nil
}

// DecodeIntent refuses noncanonical, corrupt or foreign-Home records before
// exposing any target to a future recovery operation.
func DecodeIntent(raw []byte, expectedHome datahome.Info) (Intent, error) {
	var envelope intentEnvelope
	var intent Intent
	if len(raw) > MaxIntentBytes || !canonicalJSON(raw, &envelope) ||
		envelope.SHA256 != digest(envelope.Payload) ||
		!canonicalJSON(envelope.Payload, &intent) || !validIntent(intent) {
		return Intent{}, ErrIntentInvalid
	}
	if intent.Home != expectedHome {
		return Intent{}, ErrIntentHomeChanged
	}
	return intent, nil
}

func validIntent(intent Intent) bool {
	if intent.Version != 1 || !validHome(intent.Home) ||
		!validWitness(intent.Home, intent.Before) || !validWitness(intent.Home, intent.After) {
		return false
	}
	before, after := intent.Before, intent.After
	switch intent.Operation {
	case InitialConfigure:
		return !before.Present && after.Present && after.Enabled && after.Revision == 1 &&
			isHomeBound(intent.Home, after.SecretName)
	case Rotate:
		return before.Present && after.Present && after.Enabled &&
			before.Revision < MaxRevision && after.Revision == before.Revision+1 &&
			before.SecretName != after.SecretName && isHomeBound(intent.Home, after.SecretName)
	case PromoteLegacy:
		return before.Present && before.SecretName == LegacyName &&
			after.Present && after.Enabled && before.Revision < MaxRevision &&
			after.Revision == before.Revision+1 && isHomeBound(intent.Home, after.SecretName)
	case Disconnect:
		return before.Present && before.Enabled && after.Present && !after.Enabled &&
			before.Revision < MaxRevision && after.Revision == before.Revision+1 &&
			before.SecretName == after.SecretName
	case Forget:
		return before.Present && !after.Present
	default:
		return false
	}
}

func validWitness(home datahome.Info, witness RowWitness) bool {
	if !witness.Present {
		return witness == (RowWitness{})
	}
	if witness.Revision < 1 || witness.Revision > MaxRevision {
		return false
	}
	_, err := Classify(home, witness.SecretName)
	return err == nil
}

func isHomeBound(home datahome.Info, name string) bool {
	kind, err := Classify(home, name)
	return err == nil && kind == HomeBound
}

func digest(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func canonicalJSON(raw []byte, target any) bool {
	if len(raw) == 0 || len(raw) > MaxIntentBytes {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return false
	}
	encoded, err := json.Marshal(target)
	return err == nil && bytes.Equal(encoded, raw)
}
