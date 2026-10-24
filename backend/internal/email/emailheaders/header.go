// Package emailheaders ports the bounded, offline prefetch-header metadata
// stage. It never opens a mailbox or stores a message.
package emailheaders

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
)

const MaxHeaderBytes = 128 * 1024

var ErrProtocol = errors.New("EMAIL_SYNC_PROTOCOL_FAILED")

// Metadata contains only the fields Python _header_metadata permits the sync
// prefetch stage to retain. A missing or ambiguous field stays empty.
type Metadata struct {
	SenderAddress            string `json:"sender_address"`
	MessageIDHash            string `json:"message_id_hash"`
	MessageSentDate          string `json:"message_sent_date"`
	SubjectNormalized        string `json:"subject_normalized"`
	SubjectJobSignal         bool   `json:"subject_job_signal"`
	SubjectServiceSignal     bool   `json:"subject_service_signal"`
	SubjectApplicationSignal bool   `json:"subject_application_signal"`
}

var jobPhrases = []string{
	"career opportunities", "career opportunity", "great match", "job agent",
	"job alert", "job alerts", "job for you", "job match", "job matches",
	"job posted", "jobs for you", "jobs posted", "jobs round up",
	"jobs roundup", "matched jobs", "new job", "new jobs", "new roles",
	"recommended jobs", "roles for you", "top applicant",
}

var servicePhrases = []string{
	"2 step", "account alert", "confirm forwarding", "forwarding confirmation",
	"login alert", "new sign in", "password", "recovery email", "security alert",
	"sign in attempt", "suspicious activity", "two step", "verification code",
	"verify your",
}

var applicationPhrases = []string{
	"application received", "application status", "application submitted",
	"application update", "interview invitation", "not moving forward",
	"thanks for applying", "thank you for applying", "we received your application",
	"your application to",
}

func values(header stdmail.Header, name string) []string {
	return textproto.MIMEHeader(header).Values(name)
}

func decodeWord(value string) string {
	decoder := mime.WordDecoder{CharsetReader: func(label string, input io.Reader) (io.Reader, error) {
		encoding, err := ianaindex.MIME.Encoding(label)
		if err != nil || encoding == nil {
			return nil, ErrProtocol
		}
		return transform.NewReader(input, encoding.NewDecoder()), nil
	}}
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func normalizedWords(value string) string {
	return strings.Join(strings.FieldsFunc(value, textcompat.IsSpace), " ")
}

func validAddress(value string) bool {
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at >= len(value)-1 || strings.Count(value, "@") != 1 || utf8.RuneCountInString(value) > 320 {
		return false
	}
	for _, r := range value {
		if textcompat.IsSpace(r) || r == '<' || r == '>' {
			return false
		}
	}
	return true
}

func sender(header stdmail.Header) string {
	from := values(header, "From")
	if len(from) != 1 {
		return ""
	}
	addresses, err := (&stdmail.AddressParser{}).ParseList(from[0])
	if err != nil || len(addresses) != 1 || addresses[0] == nil {
		return ""
	}
	address := textcompat.Fold(strings.TrimSpace(addresses[0].Address))
	if !validAddress(address) {
		return ""
	}
	return address
}

func sentDate(header stdmail.Header) string {
	dates := values(header, "Date")
	if len(dates) != 1 {
		return ""
	}
	raw := dates[0]
	parsed, err := stdmail.ParseDate(raw)
	if err != nil {
		for _, layout := range []string{
			"Mon, 2 Jan 2006 15:04:05", "2 Jan 2006 15:04:05",
			"Mon, 2 Jan 2006 15:04", "2 Jan 2006 15:04",
		} {
			if naive, parseErr := time.Parse(layout, raw); parseErr == nil {
				return naive.Format("2006-01-02")
			}
		}
		return ""
	}
	return parsed.UTC().Format("2006-01-02")
}

func containsPhrase(normalized string, phrases []string) bool {
	if normalized == "" {
		return false
	}
	padded := " " + normalized + " "
	for _, phrase := range phrases {
		if strings.Contains(padded, " "+phrase+" ") {
			return true
		}
	}
	return false
}

// Parse accepts only supplied header bytes. Failure is a fixed code, never
// remote text; no body fetch, credential lookup, persistence or logging occurs.
func Parse(raw []byte) (Metadata, error) {
	if len(raw) > MaxHeaderBytes {
		return Metadata{}, ErrProtocol
	}
	message, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Metadata{}, ErrProtocol
	}
	header := message.Header
	var subjectValues []string
	for _, value := range values(header, "Subject") {
		subjectValues = append(subjectValues, decodeWord(value))
	}
	subject := emailrouting.NormalizeSignal(strings.Join(subjectValues, " "))
	result := Metadata{
		SenderAddress:            sender(header),
		MessageSentDate:          sentDate(header),
		SubjectNormalized:        subject,
		SubjectJobSignal:         containsPhrase(subject, jobPhrases),
		SubjectServiceSignal:     containsPhrase(subject, servicePhrases),
		SubjectApplicationSignal: containsPhrase(subject, applicationPhrases),
	}
	if messageID := normalizedWords(header.Get("Message-ID")); messageID != "" {
		sum := sha256.Sum256([]byte(messageID))
		result.MessageIDHash = "sha256:" + hex.EncodeToString(sum[:])
	}
	return result, nil
}
