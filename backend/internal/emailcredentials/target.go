// Package emailcredentials validates the narrow Gmail credential target names.
// It has no secret-store access and does not authorize a credential mutation.
package emailcredentials

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"careerhub.local/backend/internal/datahome"
)

const (
	LegacyName = "job-discovery.gmail-imap.primary.v1"
	v2Prefix   = "email.v2."
	hexWidth   = 32
)

type Kind uint8

const (
	Legacy Kind = iota + 1
	HomeBound
)

var (
	ErrInvalidTarget    = errors.New("EMAIL_CONNECTION_INVALID")
	ErrGenerationFailed = errors.New("EMAIL_SECRET_TARGET_GENERATION_FAILED")
)

// Classify admits only the fixed legacy name or a v2 name bound to the exact
// admitted Home ID. It never returns an arbitrary database target to a store.
func Classify(home datahome.Info, name string) (Kind, error) {
	if !validHome(home) {
		return 0, ErrInvalidTarget
	}
	if name == LegacyName {
		return Legacy, nil
	}
	if len(name) != len(v2Prefix)+hexWidth+1+hexWidth || !strings.HasPrefix(name, v2Prefix) ||
		name[len(v2Prefix):len(v2Prefix)+hexWidth] != home.DataHomeID[3:] ||
		name[len(v2Prefix)+hexWidth] != '.' ||
		!lowerHex(name[len(v2Prefix)+hexWidth+1:]) {
		return 0, ErrInvalidTarget
	}
	return HomeBound, nil
}

// NewGenerationName produces a Home-bound target from 16 cryptographic random
// bytes. The caller remains responsible for collision checks and the recovery
// protocol before ever writing a credential.
func NewGenerationName(home datahome.Info) (string, error) {
	return newGenerationName(home, rand.Reader)
}

func newGenerationName(home datahome.Info, entropy io.Reader) (string, error) {
	if !validHome(home) {
		return "", ErrInvalidTarget
	}
	if entropy == nil {
		return "", ErrGenerationFailed
	}
	var nonce [16]byte
	if _, err := io.ReadFull(entropy, nonce[:]); err != nil {
		return "", ErrGenerationFailed
	}
	return v2Prefix + home.DataHomeID[3:] + "." + hex.EncodeToString(nonce[:]), nil
}

func validHome(home datahome.Info) bool {
	return home.FormatVersion == 2 && home.Initialized &&
		(home.Channel == "dev" || home.Channel == "stable") &&
		len(home.DataHomeID) == 3+hexWidth && strings.HasPrefix(home.DataHomeID, "dh_") &&
		lowerHex(home.DataHomeID[3:])
}

func lowerHex(value string) bool {
	if len(value) != hexWidth {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
