// Package emailoutcomes validates provider-confirmed application signals from
// already parsed email. It does not match applications or write status events.
package emailoutcomes

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/email/emailcandidates"
	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/textcompat"
)

var ErrInvalid = errors.New("EMAIL_IMPORT_OUTCOME_FAILED")

var fingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var linkedinIdentityPattern = regexp.MustCompile(`^linkedin:[0-9]+$`)
var handshakeIdentityPattern = regexp.MustCompile(`^handshake:[0-9]+$`)

const maxFieldChars = 500
const maxURLChars = 4096

type Normalized struct {
	ID                 string
	MessageFingerprint string
	Provider           string
	PostingIdentity    string
	ApplicationStatus  string
	SourceURL          string
	EvidenceCode       string
	Title              string
	Company            string
	Location           string
	ObservedAt         string
}

func plainText(value string, maximum int, required bool) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalid
	}
	normalized := strings.Join(strings.FieldsFunc(value, textcompat.IsSpace), " ")
	if (required && normalized == "") || utf8.RuneCountInString(normalized) > maximum {
		return "", ErrInvalid
	}
	for _, r := range normalized {
		if r < 32 {
			return "", ErrInvalid
		}
	}
	return normalized, nil
}

func eventTime(observedAt, recordedAt string) (string, error) {
	if _, err := time.Parse(time.RFC3339Nano, recordedAt); err != nil {
		return "", ErrInvalid
	}
	value := textcompat.Strip(observedAt)
	if value == "" {
		value = recordedAt
	}
	if strings.HasSuffix(value, "Z") {
		value = strings.TrimSuffix(value, "Z") + "+00:00"
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format("2006-01-02T15:04:05Z"), nil
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05",
		"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02",
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed.UTC().Format("2006-01-02T15:04:05Z"), nil
		}
	}
	return "", ErrInvalid
}

// Normalize accepts the closed outcome emitted by emailalerts.Parse. It
// preserves Python's exact provider evidence and posting-identity gates;
// there is no title/company matching or inferred application creation.
func Normalize(messageFingerprint string, outcome emailcandidates.ApplicationOutcome, observedAt, recordedAt string) (Normalized, error) {
	fingerprint, err := plainText(messageFingerprint, maxFieldChars, true)
	if err != nil || !fingerprintPattern.MatchString(fingerprint) {
		return Normalized{}, ErrInvalid
	}
	provider, err := plainText(outcome.SourceProvider, maxFieldChars, true)
	if err != nil {
		return Normalized{}, err
	}
	provider = textcompat.Fold(provider)
	identity, err := plainText(outcome.PostingIdentity, maxFieldChars, false)
	if err != nil {
		return Normalized{}, err
	}
	identity = textcompat.Fold(identity)
	status, err := plainText(outcome.ApplicationStatus, maxFieldChars, true)
	if err != nil {
		return Normalized{}, err
	}
	status = textcompat.Fold(status)
	if status != "applied" && status != "rejected" {
		return Normalized{}, ErrInvalid
	}
	sourceURL, err := plainText(outcome.SourceURL, maxURLChars, false)
	if err != nil {
		return Normalized{}, err
	}
	evidence, err := plainText(outcome.EvidenceCode, maxFieldChars, true)
	if err != nil {
		return Normalized{}, err
	}
	title, err := plainText(outcome.Title, maxFieldChars, false)
	if err != nil {
		return Normalized{}, err
	}
	company, err := plainText(outcome.Company, maxFieldChars, false)
	if err != nil {
		return Normalized{}, err
	}
	location, err := plainText(outcome.Location, maxFieldChars, false)
	if err != nil {
		return Normalized{}, err
	}
	posting, err := joburls.Resolve(sourceURL)
	if err != nil {
		return Normalized{}, ErrInvalid
	}
	switch provider {
	case "linkedin":
		if status != "rejected" || evidence != "linkedin_application_not_moving_forward" ||
			!linkedinIdentityPattern.MatchString(identity) || posting.Identity != identity {
			return Normalized{}, ErrInvalid
		}
	case "handshake":
		if status != "applied" || evidence != "handshake_application_sent" || company == "" {
			return Normalized{}, ErrInvalid
		}
		if identity != "" {
			if !handshakeIdentityPattern.MatchString(identity) || posting.Identity != identity {
				return Normalized{}, ErrInvalid
			}
		} else if sourceURL != "" {
			return Normalized{}, ErrInvalid
		}
	default:
		return Normalized{}, ErrInvalid
	}
	when, err := eventTime(observedAt, recordedAt)
	if err != nil {
		return Normalized{}, err
	}
	sum := sha256.Sum256([]byte(fingerprint + "\n" + identity + "\n" + status))
	return Normalized{
		ID: "email_outcome_" + hex.EncodeToString(sum[:]), MessageFingerprint: fingerprint,
		Provider: provider, PostingIdentity: identity, ApplicationStatus: status,
		SourceURL: sourceURL, EvidenceCode: evidence, Title: title,
		Company: company, Location: location, ObservedAt: when,
	}, nil
}
