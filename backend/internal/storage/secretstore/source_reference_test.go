package secretstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

type facadeSourceTrace struct {
	Action, TargetSHA string
	ValueSHA          *string
	Bytes             *int
}

func facadeDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type facadeSourceBackend struct {
	values map[string][]byte
	trace  []facadeSourceTrace
	fail   string
	cause  error
}

func (b *facadeSourceBackend) Write(target string, value []byte) error {
	hash, size := facadeDigest(value), len(value)
	b.trace = append(b.trace, facadeSourceTrace{"write", facadeDigest([]byte(target)), &hash, &size})
	if b.fail == "write" {
		return b.cause
	}
	b.values[target] = bytes.Clone(value)
	return nil
}
func (b *facadeSourceBackend) Read(target string) ([]byte, bool, error) {
	b.trace = append(b.trace, facadeSourceTrace{Action: "read", TargetSHA: facadeDigest([]byte(target))})
	if b.fail == "read" {
		return nil, false, b.cause
	}
	value, ok := b.values[target]
	return value, ok, nil
}
func (b *facadeSourceBackend) Delete(target string) (bool, error) {
	b.trace = append(b.trace, facadeSourceTrace{Action: "delete", TargetSHA: facadeDigest([]byte(target))})
	if b.fail == "delete" {
		return false, b.cause
	}
	_, ok := b.values[target]
	delete(b.values, target)
	return ok, nil
}

type facadeLimitedOutput struct {
	bytes.Buffer
	remaining int
}

func (b *facadeLimitedOutput) Write(raw []byte) (int, error) {
	if len(raw) > b.remaining {
		return 0, errors.New("reference output limit")
	}
	b.remaining -= len(raw)
	return b.Buffer.Write(raw)
}
