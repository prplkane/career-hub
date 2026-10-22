package httpapi

import (
	"bytes"
	"encoding/base64"
	"errors"
)

// Computed at runtime so no secret-shaped literal is committed.
func testExtensionCredentialBytes() []byte { return bytes.Repeat([]byte{0x3c}, 32) }

func testExtensionCredential() string {
	return base64.RawURLEncoding.EncodeToString(testExtensionCredentialBytes())
}

// memoryPairingStore stands in for the Data Home file in unit tests.
type memoryPairingStore struct {
	record  *PairingRecord
	failing bool
	saves   int
}

func (m *memoryPairingStore) Load() (PairingRecord, bool, error) {
	if m.record == nil {
		return PairingRecord{}, false, nil
	}
	return *m.record, true, nil
}

func (m *memoryPairingStore) Save(r PairingRecord) error {
	if m.failing {
		return errors.New("synthetic store failure")
	}
	m.saves++
	m.record = &r
	return nil
}

func (m *memoryPairingStore) Delete() error {
	if m.failing {
		return errors.New("synthetic store failure")
	}
	m.record = nil
	return nil
}
