// Package emailalerts composes the bounded, offline MIME and candidate seams
// into a JSON-ready alert. It never opens a mailbox, fetches a URL or writes
// Career Hub data.
package emailalerts

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"time"

	"careerhub.local/backend/internal/email/emailcandidates"
	"careerhub.local/backend/internal/email/emailmime"
	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
)

const SchemaVersion = 6

type Sender struct {
	Address string `json:"address"`
	Domain  string `json:"domain"`
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Result struct {
	SchemaVersion       int                                  `json:"schema_version"`
	MessageFingerprint  string                               `json:"message_fingerprint"`
	MessageIDHash       string                               `json:"message_id_hash"`
	Sender              Sender                               `json:"sender"`
	Subject             string                               `json:"subject"`
	ReceivedAt          string                               `json:"received_at"`
	Candidates          []emailcandidates.EnrichedCandidate  `json:"candidates"`
	ApplicationOutcomes []emailcandidates.ApplicationOutcome `json:"application_outcomes"`
	IgnoredLinkCount    int                                  `json:"ignored_link_count"`
	Warnings            []Warning                            `json:"warnings"`
}

var warningMessages = map[string]string{
	"EMAIL_CHARSET_FALLBACK": "A text part used a safe fallback character encoding.",
	"EMAIL_SENDER_MISSING":   "The sender header is missing.",
	"EMAIL_SENDER_INVALID":   "The sender header does not contain one valid address.",
	"EMAIL_DATE_INVALID":     "The received date could not be normalized.",
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func addWarning(warnings *[]Warning, code string) error {
	message, ok := warningMessages[code]
	if !ok {
		return emailmime.ErrParseFailed
	}
	for _, warning := range *warnings {
		if warning.Code == code {
			return nil
		}
	}
	*warnings = append(*warnings, Warning{Code: code, Message: message})
	return nil
}

func normalizeHeader(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r <= 0x08 || (r >= 0x0b && r <= 0x0c) || (r >= 0x0e && r <= 0x1f) || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	return strings.Join(strings.FieldsFunc(cleaned, textcompat.IsSpace), " ")
}

func decodeHeader(value string) string {
	decoder := mime.WordDecoder{CharsetReader: func(label string, input io.Reader) (io.Reader, error) {
		encoding, err := ianaindex.MIME.Encoding(label)
		if err != nil {
			return nil, err
		}
		if encoding == nil {
			return nil, emailmime.ErrParseFailed
		}
		return transform.NewReader(input, encoding.NewDecoder()), nil
	}}
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func headerValues(header stdmail.Header, key string) []string {
	return textproto.MIMEHeader(header).Values(key)
}

func validAddress(value string) bool {
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at >= len(value)-1 || strings.Count(value, "@") != 1 {
		return false
	}
	for _, r := range value {
		if r == '@' {
			continue
		}
		if textcompat.IsSpace(r) || r == '<' || r == '>' {
			return false
		}
	}
	return true
}

func senderFrom(header stdmail.Header, warnings *[]Warning) (Sender, error) {
	values := headerValues(header, "From")
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return Sender{}, addWarning(warnings, "EMAIL_SENDER_MISSING")
	}
	if len(values) != 1 {
		return Sender{}, addWarning(warnings, "EMAIL_SENDER_INVALID")
	}
	parser := stdmail.AddressParser{}
	addresses, err := parser.ParseList(values[0])
	if err != nil || len(addresses) != 1 || addresses[0] == nil {
		return Sender{}, addWarning(warnings, "EMAIL_SENDER_INVALID")
	}
	address := textcompat.Fold(strings.TrimSpace(addresses[0].Address))
	if !validAddress(address) {
		return Sender{}, addWarning(warnings, "EMAIL_SENDER_INVALID")
	}
	return Sender{Address: address, Domain: address[strings.LastIndexByte(address, '@')+1:]}, nil
}

func receivedAt(header stdmail.Header, warnings *[]Warning) (string, error) {
	raw := header.Get("Date")
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	parsed, err := stdmail.ParseDate(raw)
	if err != nil {
		// email.utils.parsedate_to_datetime also accepts zone-less dates and
		// returns a naive ISO timestamp. net/mail requires an RFC 5322 zone.
		for _, layout := range []string{
			"Mon, 2 Jan 2006 15:04:05", "2 Jan 2006 15:04:05",
			"Mon, 2 Jan 2006 15:04", "2 Jan 2006 15:04",
		} {
			if naive, parseErr := time.Parse(layout, raw); parseErr == nil {
				return naive.Format("2006-01-02T15:04:05"), nil
			}
		}
		return "", addWarning(warnings, "EMAIL_DATE_INVALID")
	}
	for _, token := range strings.Fields(raw) {
		if token == "-0000" {
			// RFC 5322's unknown local zone maps to a naive datetime in
			// Python email.utils.parsedate_to_datetime.
			return parsed.Format("2006-01-02T15:04:05"), nil
		}
	}
	// Python datetime.isoformat uses +00:00 for a parsed UTC zone, not Z.
	return parsed.Format("2006-01-02T15:04:05-07:00"), nil
}

// Parse returns Python parse_email_alert-shaped data from one supplied .eml
// byte slice. No raw message ID, recipient or body is returned.
func Parse(content []byte) (Result, error) {
	decoded, err := emailmime.Decode(content)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		SchemaVersion:       SchemaVersion,
		MessageFingerprint:  hashBytes(content),
		Candidates:          []emailcandidates.EnrichedCandidate{},
		ApplicationOutcomes: []emailcandidates.ApplicationOutcome{},
		Warnings:            []Warning{},
	}
	for _, code := range decoded.Warnings {
		if err := addWarning(&result.Warnings, code); err != nil {
			return Result{}, err
		}
	}
	result.Subject = normalizeHeader(decodeHeader(decoded.Header.Get("Subject")))
	result.Sender, err = senderFrom(decoded.Header, &result.Warnings)
	if err != nil {
		return Result{}, err
	}
	composed, err := emailcandidates.ComposeSelected(decoded, result.Sender.Address, result.Subject)
	if err != nil {
		return Result{}, err
	}
	result.Candidates = append(result.Candidates, composed.Candidates...)
	result.ApplicationOutcomes = append(result.ApplicationOutcomes, composed.ApplicationOutcomes...)
	result.IgnoredLinkCount = composed.IgnoredLinkCount
	if messageID := normalizeHeader(decoded.Header.Get("Message-ID")); messageID != "" {
		result.MessageIDHash = hashText(messageID)
	}
	result.ReceivedAt, err = receivedAt(decoded.Header, &result.Warnings)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
