package keys

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/documents/envelope"
)

type memoryBackend struct {
	values                       map[string][]byte
	trace                        []string
	readErr, writeErr, deleteErr error
	mutateWrite                  bool
}

func (b *memoryBackend) Write(target string, secret []byte) error {
	b.trace = append(b.trace, "write:"+target)
	if b.writeErr != nil {
		return b.writeErr
	}
	b.values[target] = secret // Deliberately retains the exact caller buffer.
	if b.mutateWrite {
		secret[0] = 0xff
	}
	return nil
}
func (b *memoryBackend) Read(target string) ([]byte, bool, error) {
	b.trace = append(b.trace, "read:"+target)
	value, found := b.values[target]
	return value, found, b.readErr
}
func (b *memoryBackend) Delete(target string) (bool, error) {
	b.trace = append(b.trace, "delete:"+target)
	if b.deleteErr != nil {
		return false, b.deleteErr
	}
	_, found := b.values[target]
	delete(b.values, target)
	return found, nil
}

type valueBackend struct{}

func (valueBackend) Write(string, []byte) error        { return nil }
func (valueBackend) Read(string) ([]byte, bool, error) { return nil, false, nil }
func (valueBackend) Delete(string) (bool, error)       { return false, nil }

func newTestManager(t *testing.T, channel string, backend Backend, random RandomBytes) *Manager {
	t.Helper()
	manager, err := NewManager(channel, backend, random)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func target(channel string) string {
	return "CareerHub/" + channel + "/document-key/" + testHome + "/" + testKey
}

func deterministic(size int) ([]byte, error) {
	if size == 16 {
		return bytes.Repeat([]byte{0x11}, size), nil
	}
	return bytes.Repeat([]byte{0x22}, size), nil
}

func TestConstructorRequiresExplicitBackendAndExactChannel(t *testing.T) {
	for _, channel := range []string{"", "Stable", "stable ", "preview", "\x00"} {
		manager, err := NewManager(channel, nil, nil)
		requireCode(t, err, CodeInvalidChannel)
		if manager != nil {
			t.Fatal("invalid constructor returned manager")
		}
	}
	var typedNil *memoryBackend
	for _, backend := range []Backend{nil, typedNil} {
		_, err := NewManager("stable", backend, nil)
		requireCode(t, err, CodeBackendRequired)
	}
	for _, channel := range []string{"dev", "stable"} {
		newTestManager(t, channel, valueBackend{}, nil)
	}
}

func TestCreateOrderingNamespaceAndCollisionOverwrite(t *testing.T) {
	for _, channel := range []string{"dev", "stable"} {
		t.Run(channel, func(t *testing.T) {
			backend := &memoryBackend{values: map[string][]byte{target(channel): bytes.Repeat([]byte{0x33}, KeyBytes)}}
			var trace []string
			manager := newTestManager(t, channel, backend, func(size int) ([]byte, error) {
				trace = append(trace, fmt.Sprint(size))
				return deterministic(size)
			})
			created, err := manager.Create(testHome, func(state State) error {
				trace = append(trace, "callback")
				if state != validState() || len(backend.trace) != 0 {
					t.Fatal("callback ordering/state mismatch")
				}
				return nil
			})
			if err != nil || created.State != validState() || !bytes.Equal(created.Key, bytes.Repeat([]byte{0x22}, KeyBytes)) {
				t.Fatal("create result mismatch")
			}
			if !reflect.DeepEqual(trace, []string{"16", "32", "callback"}) || !reflect.DeepEqual(backend.trace, []string{"write:" + target(channel)}) {
				t.Fatal("unexpected operation order")
			}
			if !bytes.Equal(backend.values[target(channel)], created.Key) {
				t.Fatal("existing target was not overwritten as in source")
			}
		})
	}
}

func TestCreateRejectsHomeBeforeRNG(t *testing.T) {
	manager := newTestManager(t, "stable", valueBackend{}, func(int) ([]byte, error) { t.Fatal("unexpected RNG"); return nil, nil })
	_, err := manager.Create("bad", nil)
	requireCode(t, err, CodeStateInvalid)
}

func TestCreateCallsBothRNGStepsBeforeLengthValidation(t *testing.T) {
	for _, lengths := range [][2]int{{0, 32}, {15, 32}, {17, 32}, {16, 0}, {16, 31}, {16, 33}, {17, 33}} {
		backend := &memoryBackend{values: map[string][]byte{}}
		calls := []int{}
		manager := newTestManager(t, "stable", backend, func(size int) ([]byte, error) {
			calls = append(calls, size)
			return make([]byte, lengths[len(calls)-1]), nil
		})
		result, err := manager.Create(testHome, func(State) error { t.Fatal("invalid RNG reached callback"); return nil })
		requireCode(t, err, CodeGenerationFailed)
		if !reflect.DeepEqual(calls, []int{16, 32}) || len(backend.trace) != 0 || result.Key != nil || result.State != (State{}) {
			t.Fatal("generation failure order/result mismatch")
		}
	}
}

func requireOperation(t *testing.T, err error, operation string, cause error) {
	t.Helper()
	var typed *OperationError
	if !errors.As(err, &typed) || typed.Operation != operation || !errors.Is(err, cause) {
		t.Fatal("operation failure lost its type/cause")
	}
	for _, text := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if text != "The document key operation failed." {
			t.Fatal("operation diagnostics exposed cause/internal data")
		}
	}
}

func TestCreateInjectedErrorsStopWithoutFallback(t *testing.T) {
	cause := errors.New("synthetic private backend marker")
	for _, at := range []string{"random1", "random2", "before_write", "write"} {
		t.Run(at, func(t *testing.T) {
			backend := &memoryBackend{values: map[string][]byte{}}
			if at == "write" {
				backend.writeErr = cause
			}
			calls := 0
			callbacks := 0
			manager := newTestManager(t, "stable", backend, func(size int) ([]byte, error) {
				calls++
				if at == fmt.Sprint("random", calls) {
					return make([]byte, size), cause
				}
				// Bad first length must not mask a second RNG exception.
				if at == "random2" && calls == 1 {
					return nil, nil
				}
				return deterministic(size)
			})
			result, err := manager.Create(testHome, func(State) error {
				callbacks++
				if at == "before_write" {
					return cause
				}
				return nil
			})
			op := at
			if strings.HasPrefix(at, "random") {
				op = "random"
			}
			requireOperation(t, err, op, cause)
			if result.Key != nil || result.State != (State{}) || len(backend.values) != 0 {
				t.Fatal("operation failure returned success material")
			}
			wantCalls := 2
			if at == "random1" {
				wantCalls = 1
			}
			wantCallbacks := 1
			if strings.HasPrefix(at, "random") {
				wantCallbacks = 0
			}
			wantWrites := 0
			if at == "write" {
				wantWrites = 1
			}
			if calls != wantCalls || callbacks != wantCallbacks || len(backend.trace) != wantWrites {
				t.Fatal("operation failure did extra work")
			}
		})
	}
}

func TestCreateSnapshotsEveryMutableBoundary(t *testing.T) {
	buffer := bytes.Repeat([]byte{0x11}, 32)
	backend := &memoryBackend{values: map[string][]byte{}, mutateWrite: true}
	manager := newTestManager(t, "stable", backend, func(size int) ([]byte, error) {
		if size == 16 {
			return buffer[:16], nil
		}
		for i := range buffer {
			buffer[i] = 0x22
		}
		return buffer, nil
	})
	created, err := manager.Create(testHome, func(State) error {
		for i := range buffer {
			buffer[i] = 0x33
		}
		return nil
	})
	if err != nil || created.State != validState() || !bytes.Equal(created.Key, bytes.Repeat([]byte{0x22}, KeyBytes)) {
		t.Fatal("RNG/callback/backend mutation leaked into result")
	}
	created.Key[1] = 0x44
	if backend.values[target("stable")][1] != 0x22 {
		t.Fatal("returned key aliases backend storage")
	}
}

func TestLoadRetainedKeysOwnershipAndFailurePrecedence(t *testing.T) {
	backend := &memoryBackend{values: map[string][]byte{target("dev"): bytes.Repeat([]byte{0x22}, KeyBytes)}}
	manager := newTestManager(t, "dev", backend, func(int) ([]byte, error) { t.Fatal("load invoked RNG"); return nil, nil })
	loaded, err := manager.Load(validState())
	if err != nil || loaded.State != validState() || len(loaded.Key) != KeyBytes {
		t.Fatal("load failed")
	}
	loaded.Key[0] = 0xff
	if backend.values[target("dev")][0] != 0x22 {
		t.Fatal("loaded key aliases storage")
	}
	retained, err := manager.LoadKey(testHome, testKey)
	if err != nil || retained[0] != 0x22 {
		t.Fatal("retained key lookup failed")
	}
	backend.values[target("dev")][1] = 0xee
	if retained[1] != 0x22 {
		t.Fatal("storage mutation changed returned key")
	}
	_, err = manager.LoadKey(testHome, "key_22222222222222222222222222222222")
	requireCode(t, err, CodeUnavailable)
	_, err = manager.LoadKey("dh_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", testKey)
	requireCode(t, err, CodeUnavailable)
	_, err = newTestManager(t, "stable", backend, nil).LoadKey(testHome, testKey)
	requireCode(t, err, CodeUnavailable)
	_, err = manager.LoadKey("bad", testKey)
	requireCode(t, err, CodeStateInvalid)
	_, err = manager.LoadKey(testHome, "bad")
	requireCode(t, err, CodeStateInvalid)
	for _, invalid := range [][]byte{nil, {}, []byte("short"), make([]byte, 33)} {
		backend.values[target("dev")] = invalid
		result, err := manager.Load(validState())
		requireCode(t, err, CodeKeyInvalid)
		if result.Key != nil || result.State != (State{}) {
			t.Fatal("invalid load returned result")
		}
	}
	cause := errors.New("synthetic private read marker")
	backend.readErr = cause
	for _, exists := range []bool{true, false} {
		if !exists {
			delete(backend.values, target("dev"))
		}
		key, err := manager.LoadKey(testHome, testKey)
		requireOperation(t, err, "read", cause)
		if key != nil {
			t.Fatal("read failure returned key")
		}
	}
}

func TestDiscardUsesExactTargetAndIgnoresAbsentFlag(t *testing.T) {
	backend := &memoryBackend{values: map[string][]byte{target("dev"): make([]byte, KeyBytes)}}
	manager := newTestManager(t, "dev", backend, nil)
	for range 2 {
		if err := manager.DiscardUnpublished(validState()); err != nil {
			t.Fatal(err)
		}
	}
	if len(backend.values) != 0 || !reflect.DeepEqual(backend.trace, []string{"delete:" + target("dev"), "delete:" + target("dev")}) {
		t.Fatal("discard target/order mismatch")
	}
	cause := errors.New("synthetic private delete marker")
	backend.deleteErr = cause
	requireOperation(t, manager.DiscardUnpublished(validState()), "delete", cause)
}

func TestDefaultRandomAndExistingEnvelopeInteroperation(t *testing.T) {
	backend := &memoryBackend{values: map[string][]byte{}}
	manager := newTestManager(t, "stable", backend, nil)
	created, err := manager.Create(testHome, nil)
	if err != nil || len(created.Key) != KeyBytes || !validID(created.State.ActiveKeyID, "key_") {
		t.Fatal("secure default generation failed")
	}
	loaded, err := manager.Load(created.State)
	if err != nil || !bytes.Equal(loaded.Key, created.Key) {
		t.Fatal("created key could not be loaded")
	}
	context := envelope.Context{DataHomeID: testHome, KeyID: created.State.ActiveKeyID, Purpose: "ledger", ObjectID: "ledger"}
	sealed, err := envelope.Encrypt([]byte("synthetic ledger"), created.Key, context)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := envelope.Decrypt(sealed, loaded.Key, context)
	if err != nil || string(opened.Plaintext) != "synthetic ledger" {
		t.Fatal("existing envelope interoperability failed")
	}
	context.DataHomeID = "dh_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if result, err := envelope.Decrypt(sealed, loaded.Key, context); err == nil || result.Plaintext != nil {
		t.Fatal("cross-home context was accepted")
	}
}
