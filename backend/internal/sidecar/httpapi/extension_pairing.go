package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Extension pairing (ADR SEC-250 item 4). The extension used to prove itself
// with its Origin header alone, which a local program can forge. Now the owner
// pairs it once: the desktop window shows a short code, the owner types it into
// the extension, and the extension receives a long-lived credential. Only the
// credential's SHA-256 is stored. The Origin check stays as a second layer.
const (
	ExtensionCredentialHeader = "X-Career-Hub-Extension-Credential"

	extensionPairPath        = "/api/extension/pair"
	extensionPairingPath     = "/api/extension/pairing"
	extensionPairingCodePath = "/api/extension/pairing-code"

	pairingCodeLength   = 8
	pairingCodeLifetime = 5 * time.Minute
	pairingMaxAttempts  = 5
)

// pairingAlphabet omits look-alike characters (0/O, 1/I/L) so a code read off
// the screen is typed correctly.
const pairingAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// PairingRecord is what persists: never the credential itself.
type PairingRecord struct {
	CredentialSHA256 [32]byte
	PairedAt         time.Time
}

// PairingStore persists at most one pairing in the private Data Home. The
// composition root supplies it; this package does no file access.
type PairingStore interface {
	Load() (PairingRecord, bool, error)
	Save(PairingRecord) error
	Delete() error
}

type extensionPairing struct {
	mu       sync.Mutex
	store    PairingStore
	paired   *PairingRecord
	code     string
	expires  time.Time
	attempts int
	now      func() time.Time
	random   io.Reader
}

// ConfigureExtensionPairing attaches the pairing store to a handler built with
// extension origins. It loads any existing pairing; a load failure leaves the
// extension unpaired (fail closed) rather than refusing startup.
func ConfigureExtensionPairing(h http.Handler, store PairingStore) error {
	typed, ok := h.(*handler)
	if !ok || store == nil || typed.extensions == nil || typed.pairing != nil {
		return &Error{Code: CodeConfiguration}
	}
	p := &extensionPairing{store: store, now: time.Now, random: rand.Reader}
	if record, found, err := store.Load(); err == nil && found {
		p.paired = &record
	}
	typed.pairing = p
	return nil
}

// credentialMatches reports whether a request carries the paired credential.
func (p *extensionPairing) credentialMatches(r *http.Request) bool {
	if p == nil {
		return false
	}
	value, single := oneHeader(r, ExtensionCredentialHeader)
	if !single || value == "" {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return false
	}
	sum := sha256.Sum256(decoded)
	clear(decoded)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paired != nil && subtle.ConstantTimeCompare(sum[:], p.paired.CredentialSHA256[:]) == 1
}

func (p *extensionPairing) newCode() (string, time.Time, error) {
	var b strings.Builder
	limit := big.NewInt(int64(len(pairingAlphabet)))
	for i := 0; i < pairingCodeLength; i++ {
		n, err := rand.Int(p.random, limit) // uniform, no modulo bias
		if err != nil {
			return "", time.Time{}, err
		}
		b.WriteByte(pairingAlphabet[n.Int64()])
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.code, p.expires, p.attempts = b.String(), p.now().Add(pairingCodeLifetime), 0
	return p.code, p.expires, nil
}

// redeem exchanges the pending code for a new credential. Any failure counts
// as an attempt; too many void the code, so wrong answers are not an oracle.
func (p *extensionPairing) redeem(code string) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.code == "" || !p.now().Before(p.expires) {
		p.code = ""
		return "", "EXTENSION_PAIRING_CODE_EXPIRED"
	}
	if subtle.ConstantTimeCompare([]byte(normalizePairingCode(code)), []byte(p.code)) != 1 {
		p.attempts++
		if p.attempts >= pairingMaxAttempts {
			p.code = ""
		}
		return "", "EXTENSION_PAIRING_CODE_INVALID"
	}
	p.code = "" // single use
	var secret [32]byte
	if _, err := io.ReadFull(p.random, secret[:]); err != nil {
		return "", "EXTENSION_PAIRING_UNAVAILABLE"
	}
	record := PairingRecord{CredentialSHA256: sha256.Sum256(secret[:]), PairedAt: p.now().UTC()}
	if err := p.store.Save(record); err != nil {
		clear(secret[:])
		return "", "EXTENSION_PAIRING_UNAVAILABLE"
	}
	p.paired = &record
	credential := base64.RawURLEncoding.EncodeToString(secret[:])
	clear(secret[:])
	return credential, ""
}

func (p *extensionPairing) revoke() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.code = ""
	if err := p.store.Delete(); err != nil {
		return err
	}
	p.paired = nil
	return nil
}

func normalizePairingCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
}

// servePairingAdmin answers the window's pairing routes. authorize already
// required the UI token; agent and extension tokens never reach these paths.
func (h *handler) servePairingAdmin(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != extensionPairingCodePath && path != extensionPairingPath {
		return false
	}
	if h.pairing == nil {
		if path == extensionPairingPath && r.Method == http.MethodGet {
			sendJSON(w, map[string]any{"enabled": false, "paired": false})
		} else {
			sendError(w, http.StatusConflict, "EXTENSION_NOT_ENABLED", "Enable the browser extension before pairing it.")
		}
		return true
	}
	switch {
	case path == extensionPairingCodePath && r.Method == http.MethodPost:
		code, expires, err := h.pairing.newCode()
		if err != nil {
			sendError(w, http.StatusServiceUnavailable, "EXTENSION_PAIRING_UNAVAILABLE", "A pairing code could not be created.")
			return true
		}
		sendJSON(w, map[string]any{"code": code[:4] + "-" + code[4:], "expires_at": expires.UTC().Format(time.RFC3339)})
	case path == extensionPairingPath && r.Method == http.MethodGet:
		h.pairing.mu.Lock()
		status := map[string]any{"enabled": true, "paired": h.pairing.paired != nil}
		if h.pairing.paired != nil {
			status["paired_at"] = h.pairing.paired.PairedAt.UTC().Format(time.RFC3339)
		}
		h.pairing.mu.Unlock()
		sendJSON(w, status)
	case path == extensionPairingPath && r.Method == http.MethodDelete:
		if err := h.pairing.revoke(); err != nil {
			sendError(w, http.StatusServiceUnavailable, "EXTENSION_PAIRING_UNAVAILABLE", "The pairing could not be revoked.")
			return true
		}
		sendJSON(w, map[string]any{"enabled": true, "paired": false})
	default:
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
	}
	return true
}

// serveExtensionPair redeems a code for the configured extension Origin.
func (h *handler) serveExtensionPair(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		sendError(w, http.StatusConflict, "EXTENSION_NOT_ENABLED", "Pairing is not enabled.")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 256))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || len(body.Code) > 32 {
		sendError(w, http.StatusBadRequest, "EXTENSION_PAIRING_REQUEST_INVALID", "The pairing request is invalid.")
		return
	}
	credential, code := h.pairing.redeem(body.Code)
	if code != "" {
		sendError(w, http.StatusForbidden, code, "The pairing code is not valid. Create a new code in Career Hub Settings.")
		return
	}
	sendJSON(w, map[string]any{"contract_version": 1, "client": "extension", "credential": credential})
}
