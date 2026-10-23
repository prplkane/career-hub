package keys

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"reflect"
)

// Backend must be explicitly supplied. Read's found flag distinguishes an
// absent entry from an existing empty/invalid key. Errors always take precedence
// over the accompanying value/flag. Backend implementations own their storage.
type Backend interface {
	Write(target string, secret []byte) error
	Read(target string) (secret []byte, found bool, err error)
	Delete(target string) (deleted bool, err error)
}

type RandomBytes func(size int) ([]byte, error)

// MasterKey owns its returned key bytes. State contains no mutable references.
type MasterKey struct {
	State State
	Key   []byte
}

// OperationError distinguishes injected/native-adapter failures from domain
// failures. Its fixed message never formats the cause. Unwrap is available for
// deliberate typed inspection; callers must not log an unwrapped secret-bearing
// backend error. Such errors receive no source-domain parity credit.
type OperationError struct {
	Operation string
	cause     error
}

func (e *OperationError) Error() string { return "The document key operation failed." }
func (e *OperationError) Unwrap() error { return e.cause }
func (e *OperationError) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, e.Error())
}

func operationError(operation string, cause error) error {
	return &OperationError{Operation: operation, cause: cause}
}

// Manager holds only its immutable configuration and injected adapters. It does
// not cache keys. Concurrent calls require a concurrency-safe backend/RNG/callback.
type Manager struct {
	channel string
	backend Backend
	random  RandomBytes
}

// NewManager never discovers or selects a native backend. Nil randomness uses
// crypto/rand; nil (including typed-nil) backend is an explicit configuration error.
func NewManager(channel string, backend Backend, random RandomBytes) (*Manager, error) {
	if channel != "dev" && channel != "stable" {
		return nil, &Error{Code: CodeInvalidChannel}
	}
	if nilBackend(backend) {
		return nil, &Error{Code: CodeBackendRequired}
	}
	if random == nil {
		random = secureRandom
	}
	return &Manager{channel: channel, backend: backend, random: random}, nil
}

func nilBackend(backend Backend) bool {
	if backend == nil {
		return true
	}
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}

func secureRandom(size int) ([]byte, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return nil, err
	}
	return data, nil
}

func (m *Manager) target(homeID, keyID string) (string, error) {
	if err := validateIdentity(homeID, keyID); err != nil {
		return "", err
	}
	return "CareerHub/" + m.channel + "/document-key/" + homeID + "/" + keyID, nil
}

// Create preserves source ordering: validate home, RNG(16), RNG(32), validate
// both lengths, callback, backend write. A callback error forbids the write.
// No rollback or regeneration is attempted after any operation error.
func (m *Manager) Create(homeID string, beforeWrite func(State) error) (MasterKey, error) {
	if err := validateIdentity(homeID, "key_00000000000000000000000000000000"); err != nil {
		return MasterKey{}, err
	}
	idData, err := m.random(16)
	if err != nil {
		return MasterKey{}, operationError("random", err)
	}
	idValid := len(idData) == 16
	// Snapshot before the next injected operation can mutate its returned buffer.
	// Do not allocate a copy of an oversized invalid adapter result.
	if idValid {
		idData = bytes.Clone(idData)
	}
	keyData, err := m.random(KeyBytes)
	if err != nil {
		return MasterKey{}, operationError("random", err)
	}
	if !idValid || len(keyData) != KeyBytes {
		return MasterKey{}, &Error{Code: CodeGenerationFailed}
	}
	key := bytes.Clone(keyData)
	state := State{
		Version: Version, Algorithm: Algorithm, DataHomeID: homeID,
		ActiveKeyID: "key_" + hex.EncodeToString(idData),
	}
	if beforeWrite != nil {
		if err := beforeWrite(state); err != nil {
			return MasterKey{}, operationError("before_write", err)
		}
	}
	target, err := m.target(homeID, state.ActiveKeyID)
	if err != nil {
		return MasterKey{}, err
	}
	// The backend may retain or mutate its input without affecting the result.
	if err := m.backend.Write(target, bytes.Clone(key)); err != nil {
		return MasterKey{}, operationError("write", err)
	}
	return MasterKey{State: state, Key: key}, nil
}

func (m *Manager) Load(state State) (MasterKey, error) {
	if err := state.validate(); err != nil {
		return MasterKey{}, err
	}
	key, err := m.LoadKey(state.DataHomeID, state.ActiveKeyID)
	if err != nil {
		return MasterKey{}, err
	}
	return MasterKey{State: state, Key: key}, nil
}

// LoadKey resolves an active or retained rotation key without any fallback.
func (m *Manager) LoadKey(homeID, keyID string) ([]byte, error) {
	target, err := m.target(homeID, keyID)
	if err != nil {
		return nil, err
	}
	key, found, err := m.backend.Read(target)
	if err != nil {
		return nil, operationError("read", err)
	}
	if !found {
		return nil, &Error{Code: CodeUnavailable}
	}
	if len(key) != KeyBytes {
		return nil, &Error{Code: CodeKeyInvalid}
	}
	return bytes.Clone(key), nil
}

// DiscardUnpublished is only for an explicitly known, never-published key. It
// performs no publication check itself; it must not be exposed as general deletion.
func (m *Manager) DiscardUnpublished(state State) error {
	if err := state.validate(); err != nil {
		return err
	}
	target, err := m.target(state.DataHomeID, state.ActiveKeyID)
	if err != nil {
		return err
	}
	if _, err := m.backend.Delete(target); err != nil {
		return operationError("delete", err)
	}
	return nil
}
