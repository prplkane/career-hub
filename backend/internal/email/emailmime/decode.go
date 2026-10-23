// Package emailmime provides bounded, offline MIME body selection for email
// alerts. It does not classify jobs, open a mailbox, or write application data.
package emailmime

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
)

const (
	MaxEmailBytes       = 10 * 1024 * 1024
	MaxMIMEParts        = 200
	MaxNestedDepth      = 10
	MaxMIMEDepth        = 32               // All child edges, including multipart containers.
	MaxParsedBodyBytes  = 32 * 1024 * 1024 // Cumulative copied/decoded body bytes, not peak heap.
	MaxDecodedTextChars = 500_000

	WarningCharsetFallback = "EMAIL_CHARSET_FALLBACK"
)

// Error is a fixed, content-free code matching the Python source boundary.
type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrEmpty        Error = "EMAIL_EMPTY"
	ErrTooLarge     Error = "EMAIL_TOO_LARGE"
	ErrPartLimit    Error = "EMAIL_MIME_LIMIT_EXCEEDED"
	ErrNestingLimit Error = "EMAIL_NESTING_LIMIT_EXCEEDED"
	ErrParseBudget  Error = "EMAIL_MIME_BYTES_LIMIT_EXCEEDED"
	ErrTextLimit    Error = "EMAIL_TEXT_LIMIT_EXCEEDED"
	ErrParseFailed  Error = "EMAIL_PARSE_FAILED"
)

type BodyPart struct {
	ContentType string
	Text        string
}

// Result contains all non-attachment text selected by the source's MIME rules.
// Headers are retained only for a later offline metadata/parser stage; callers
// must not log or persist them without an explicit privacy contract.
type Result struct {
	Header       stdmail.Header
	Selected     []BodyPart
	Warnings     []string
	AllTextChars int
}

type node struct {
	contentType string
	charset     string
	transfer    string
	attachment  bool
	children    []*node
	raw         []byte
	text        string
}

type state struct {
	parts              int
	remainingBodyBytes int
	allTextChars       int
	warnings           []string
}

var filenameParameter = regexp.MustCompile(`(?i)(?:^|;)\s*(?:file)?name(?:\*\d+\*?|\*)?\s*=`)

func (s *state) warn(code string) {
	for _, existing := range s.warnings {
		if existing == code {
			return
		}
	}
	s.warnings = append(s.warnings, code)
}

// Decode validates one complete message, including unselected alternatives,
// before exposing selected body parts. Input is never fetched by this package.
func Decode(content []byte) (Result, error) {
	if len(content) == 0 {
		return Result{}, ErrEmpty
	}
	if len(content) > MaxEmailBytes {
		return Result{}, ErrTooLarge
	}
	st := &state{remainingBodyBytes: MaxParsedBodyBytes}
	header, body, err := readMessage(content, st)
	if err != nil {
		return Result{}, parseError(err)
	}
	root, err := parseNode(textproto.MIMEHeader(header), body, 0, 0, st)
	if err != nil {
		return Result{}, err
	}
	if err := decodeTextLeaves(root, st); err != nil {
		return Result{}, err
	}
	return Result{
		Header: header, Selected: selectParts(root), Warnings: st.warnings,
		AllTextChars: st.allTextChars,
	}, nil
}

// Bound each allocation before reading. Nested copies and transfer decoding
// consume one shared budget; a one-byte probe distinguishes exact EOF from
// overflow. This is not a measurement or cap of the Go runtime's total heap.
func (s *state) readBody(reader io.Reader) ([]byte, error) {
	if s.remainingBodyBytes < 0 {
		return nil, ErrParseBudget
	}
	body, err := io.ReadAll(io.LimitReader(reader, int64(s.remainingBodyBytes)+1))
	if len(body) > s.remainingBodyBytes {
		s.remainingBodyBytes = 0
		return nil, ErrParseBudget
	}
	s.remainingBodyBytes -= len(body)
	return body, err
}

func parseError(err error) error {
	if errors.Is(err, ErrParseBudget) {
		return ErrParseBudget
	}
	return ErrParseFailed
}

func readMessage(raw []byte, st *state) (stdmail.Header, []byte, error) {
	message, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	body, err := st.readBody(message.Body)
	if err != nil {
		return nil, nil, err
	}
	return message.Header, body, nil
}

func mediaInfo(header textproto.MIMEHeader) (string, map[string]string) {
	value := header.Get("Content-Type")
	if value == "" {
		return "text/plain", nil
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || !strings.Contains(mediaType, "/") {
		// Python's get_content_type defaults invalid, non-multipart types to
		// text/plain. A malformed multipart declaration is not safe to treat
		// as plain body text.
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "multipart/") {
			return "multipart/invalid", nil
		}
		return "text/plain", nil
	}
	return strings.ToLower(mediaType), params
}

func isAttachment(header textproto.MIMEHeader, typeParams map[string]string) bool {
	rawDisposition := header.Get("Content-Disposition")
	// The Python source still recognizes attachment/filename in some
	// malformed parameter headers that mime.ParseMediaType rejects. Never
	// surface such a part as text merely because that parser was stricter.
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawDisposition)), "attachment") ||
		filenameParameter.MatchString(rawDisposition) ||
		filenameParameter.MatchString(header.Get("Content-Type")) {
		return true
	}
	disposition, params, err := mime.ParseMediaType(rawDisposition)
	if err == nil {
		if strings.EqualFold(disposition, "attachment") {
			return true
		}
		if _, present := params["filename"]; present {
			return true
		}
	}
	if _, present := typeParams["name"]; present {
		return true
	}
	return false
}

func parseNode(header textproto.MIMEHeader, body []byte, depth, structureDepth int, st *state) (*node, error) {
	if structureDepth > MaxMIMEDepth {
		return nil, ErrNestingLimit
	}
	st.parts++
	if st.parts > MaxMIMEParts {
		return nil, ErrPartLimit
	}
	contentType, params := mediaInfo(header)
	n := &node{
		contentType: contentType,
		charset:     params["charset"],
		transfer:    header.Get("Content-Transfer-Encoding"),
		attachment:  isAttachment(header, params),
	}
	if strings.HasPrefix(contentType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, ErrParseFailed
		}
		n.children = []*node{}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, ErrParseFailed
			}
			if err := st.admitChild(structureDepth); err != nil {
				return nil, err
			}
			partBody, err := st.readBody(part)
			if err != nil {
				return nil, parseError(err)
			}
			child, err := parseNode(part.Header, partBody, depth, structureDepth+1, st)
			if err != nil {
				return nil, err
			}
			n.children = append(n.children, child)
		}
		if len(n.children) == 0 {
			return nil, ErrParseFailed
		}
		return n, nil
	}
	if contentType == "message/rfc822" {
		if depth >= MaxNestedDepth {
			return nil, ErrNestingLimit
		}
		if err := st.admitChild(structureDepth); err != nil {
			return nil, err
		}
		decoded, err := decodeTransfer(n.transfer, body, st)
		if err != nil {
			return nil, parseError(err)
		}
		childHeader, childBody, err := readMessage(decoded, st)
		if err != nil {
			return nil, parseError(err)
		}
		child, err := parseNode(textproto.MIMEHeader(childHeader), childBody, depth+1, structureDepth+1, st)
		if err != nil {
			return nil, err
		}
		n.children = []*node{child}
		return n, nil
	}
	n.raw = body
	return n, nil
}

func (s *state) admitChild(structureDepth int) error {
	if structureDepth >= MaxMIMEDepth {
		return ErrNestingLimit
	}
	if s.parts >= MaxMIMEParts {
		return ErrPartLimit
	}
	return nil
}

func decodeTransfer(transfer string, raw []byte, st *state) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(transfer)) {
	case "base64":
		decoder := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(raw))
		return st.readBody(decoder)
	case "quoted-printable":
		return st.readBody(quotedprintable.NewReader(bytes.NewReader(raw)))
	default:
		return raw, nil
	}
}

func decodeTextLeaves(n *node, st *state) error {
	if n.attachment {
		return nil
	}
	if n.children != nil {
		for _, child := range n.children {
			if err := decodeTextLeaves(child, st); err != nil {
				return err
			}
		}
		return nil
	}
	if n.contentType != "text/plain" && n.contentType != "text/html" {
		return nil
	}
	encoded, err := decodeTransfer(n.transfer, n.raw, st)
	if err != nil {
		return parseError(err)
	}
	decodedOK := false
	if n.charset != "" {
		if decoded, err := decodeCharset(encoded, n.charset); err == nil {
			n.text = decoded
			decodedOK = true
		} else {
			st.warn(WarningCharsetFallback)
		}
	}
	if !decodedOK {
		for _, fallback := range []string{"utf-8", "windows-1252", "latin-1"} {
			decoded, err := decodeCharset(encoded, fallback)
			if err != nil {
				continue
			}
			if n.charset == "" && fallback != "utf-8" {
				st.warn(WarningCharsetFallback)
			}
			n.text = decoded
			decodedOK = true
			break
		}
	}
	if !decodedOK {
		return ErrParseFailed
	}
	st.allTextChars += utf8.RuneCountInString(n.text)
	if st.allTextChars > MaxDecodedTextChars {
		return ErrTextLimit
	}
	return nil
}

func decodeCharset(raw []byte, name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "utf-8", "utf8":
		if !utf8.Valid(raw) {
			return "", ErrParseFailed
		}
		return string(raw), nil
	case "us-ascii", "ascii":
		for _, b := range raw {
			if b >= 0x80 {
				return "", ErrParseFailed
			}
		}
		return string(raw), nil
	case "latin-1", "latin1", "iso-8859-1":
		var out strings.Builder
		for _, b := range raw {
			out.WriteRune(rune(b))
		}
		return out.String(), nil
	case "windows-1252", "cp1252":
		var out strings.Builder
		for _, b := range raw {
			r := charmap.Windows1252.DecodeByte(b)
			if r == utf8.RuneError {
				return "", ErrParseFailed
			}
			out.WriteRune(r)
		}
		return out.String(), nil
	default:
		encoding, err := ianaindex.IANA.Encoding(name)
		if err != nil || encoding == nil {
			return "", ErrParseFailed
		}
		decoded, err := encoding.NewDecoder().Bytes(raw)
		if err != nil || !utf8.Valid(decoded) {
			return "", ErrParseFailed
		}
		return string(decoded), nil
	}
}

func selectParts(n *node) []BodyPart {
	if n.attachment {
		return nil
	}
	if n.children != nil {
		var selected []BodyPart
		for _, child := range n.children {
			selected = append(selected, selectParts(child)...)
		}
		if n.contentType == "multipart/alternative" {
			for i := len(selected) - 1; i >= 0; i-- {
				if selected[i].ContentType == "text/html" {
					return selected[i : i+1]
				}
			}
			for i := len(selected) - 1; i >= 0; i-- {
				if selected[i].ContentType == "text/plain" {
					return selected[i : i+1]
				}
			}
			return nil
		}
		return selected
	}
	if n.contentType == "text/plain" || n.contentType == "text/html" {
		return []BodyPart{{ContentType: n.contentType, Text: n.text}}
	}
	return nil
}
