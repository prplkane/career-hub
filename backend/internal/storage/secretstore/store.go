package secretstore

import (
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	MaxSecretBytes         = 512
	CodeChannelInvalid     = "SECRET_CHANNEL_INVALID"
	CodeNameInvalid        = "SECRET_NAME_INVALID"
	CodeValueEmpty         = "SECRET_VALUE_EMPTY"
	CodeValueTooLarge      = "SECRET_VALUE_TOO_LARGE"
	CodeValueInvalidUTF8   = "SECRET_VALUE_INVALID_UTF8"
	CodeStoredValueInvalid = "SECRET_STORED_VALUE_INVALID"
	CodeBackendRequired    = "SECRET_BACKEND_REQUIRED"
)

// ValidationError exposes only a fixed validation category, never inputs.
type ValidationError struct{ Code string }

func (*ValidationError) Error() string                { return "secret store validation failed" }
func (e *ValidationError) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }

// BackendOperationError preserves error identity for trusted code while keeping
// arbitrary backend text out of Error/Format. Never format its unwrapped cause
// into a user-facing diagnostic.
type BackendOperationError struct {
	Operation string
	cause     error
}

func (*BackendOperationError) Error() string                { return "secret store backend operation failed" }
func (e *BackendOperationError) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func (e *BackendOperationError) Unwrap() error              { return e.cause }

type Store struct {
	channel string
	backend Backend
}

// New requires an explicit backend. Platform selection is not this facade's job.
func New(channel string, backend Backend) (*Store, error) {
	if channel != "stable" && channel != "dev" {
		return nil, &ValidationError{CodeChannelInvalid}
	}
	if IsNilBackend(backend) {
		return nil, &ValidationError{CodeBackendRequired}
	}
	return &Store{channel: channel, backend: backend}, nil
}

func asciiAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func (s *Store) target(name string) (string, error) {
	if len(name) < 1 || len(name) > 80 || !asciiAlphanumeric(name[0]) {
		return "", &ValidationError{CodeNameInvalid}
	}
	for i := 1; i < len(name); i++ {
		if !asciiAlphanumeric(name[i]) && name[i] != '.' && name[i] != '_' && name[i] != '-' {
			return "", &ValidationError{CodeNameInvalid}
		}
	}
	if s == nil || IsNilBackend(s.backend) {
		return "", &ValidationError{CodeBackendRequired}
	}
	return "CareerHub/" + s.channel + "/provider/" + name, nil
}

func (s *Store) Set(name, value string) error {
	// Python evaluates the target argument before encoding the secret argument.
	target, err := s.target(name)
	if err != nil {
		return err
	}
	if value == "" {
		return &ValidationError{CodeValueEmpty}
	}
	if !utf8.ValidString(value) {
		return &ValidationError{CodeValueInvalidUTF8}
	}
	if len(value) > MaxSecretBytes {
		return &ValidationError{CodeValueTooLarge}
	}
	if err := s.backend.Write(target, []byte(value)); err != nil {
		return &BackendOperationError{Operation: "write", cause: err}
	}
	return nil
}

func (s *Store) Get(name string) (string, bool, error) {
	target, err := s.target(name)
	if err != nil {
		return "", false, err
	}
	value, found, err := s.backend.Read(target)
	if err != nil {
		return "", false, &BackendOperationError{Operation: "read", cause: err}
	}
	if !found {
		return "", false, nil
	}
	if !utf8.Valid(value) {
		return "", false, &ValidationError{CodeStoredValueInvalid}
	}
	// Read compatibility intentionally has no facade 512-byte cap. The returned
	// string owns its bytes independently of the backend's mutable buffer.
	return string(value), true, nil
}

func (s *Store) Delete(name string) (bool, error) {
	target, err := s.target(name)
	if err != nil {
		return false, err
	}
	deleted, err := s.backend.Delete(target)
	if err != nil {
		return false, &BackendOperationError{Operation: "delete", cause: err}
	}
	return deleted, nil
}
