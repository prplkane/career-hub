// Package vaultbundle encodes and strictly decodes the one-file Career Vault
// export. It is pure: it never reads or writes disk and never turns a name from
// a bundle into a filesystem path. Names are a closed enum mapped to fixed
// application paths.
//
// The SHA-256 digests detect accidental damage (a truncated download, a bad
// copy). They are not a signature: anyone who edits the file can recompute them.
package vaultbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
)

const (
	Format        = "career-hub-vault-bundle"
	FormatVersion = 1
	// MaxBundleBytes bounds the whole bundle file. Base64 inside the JSON import
	// request grows it by 4/3 (16 MiB -> ~21.4 MiB), which still fits the 24 MiB
	// setup request limit. Export refuses anything import would refuse.
	MaxBundleBytes = 16 << 20
	// FileExtension is the download suffix the setup picker accepts.
	FileExtension = ".chvault"
)

// Error carries a stable code only; never contents, names of people or paths.
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

const (
	CodeInvalid               = "VAULT_BUNDLE_INVALID"
	CodeDamaged               = "VAULT_BUNDLE_DAMAGED"
	CodeVersionUnsupported    = "VAULT_BUNDLE_VERSION_UNSUPPORTED"
	CodeProtectionUnsupported = "VAULT_BUNDLE_PROTECTION_UNSUPPORTED"
	CodeLimit                 = "VAULT_BUNDLE_LIMIT"
)

type entry struct {
	name, path string
	optional   bool
}

// Fixed order: Encode writes this order so the same vault gives the same bytes.
var entries = [...]entry{
	{"profile.yaml", "career_vault/profile.yaml", false},
	{"education.yaml", "career_vault/education.yaml", false},
	{"visa_profile.yaml", "career_vault/visa_profile.yaml", false},
	{"experience_blocks.yaml", "career_vault/experience_blocks.yaml", false},
	{"project_blocks.yaml", "career_vault/project_blocks.yaml", false},
	{"skill_blocks.yaml", "career_vault/skill_blocks.yaml", false},
	{"proof_blocks.yaml", "career_vault/proof_blocks.yaml", false},
	{"style_rules.yaml", "career_vault/style_rules.yaml", false},
	{"taxonomy/skill_groups.yaml", "career_vault/taxonomy/skill_groups.yaml", false},
	{"taxonomy/technologies.yaml", "career_vault/taxonomy/technologies.yaml", false},
	{"application_answers.yaml", "career_vault/application_answers.yaml", true},
}

// Paths returns a fresh copy of every application path a bundle may carry, in
// bundle order. Optional reports whether a path may be absent from a bundle.
func Paths() []string {
	paths := make([]string, len(entries))
	for index, item := range entries {
		paths[index] = item.path
	}
	return paths
}

// Optional reports whether a known application path may be left out.
func Optional(path string) bool {
	for _, item := range entries {
		if item.path == path {
			return item.optional
		}
	}
	return false
}

// FileName is the suggested download name for an export made at now.
func FileName(now time.Time) string {
	return "career-vault-export-" + now.Format("2006-01-02") + FileExtension
}

func maxEncoded(size int) int { return ((size + 2) / 3) * 4 }

// Encode writes the bundle for files keyed by application path. It refuses
// unknown paths, missing required files and anything Decode would refuse.
func Encode(files map[string][]byte) ([]byte, *Error) {
	known := 0
	for _, item := range entries {
		if _, present := files[item.path]; present {
			known++
		}
	}
	if known != len(files) {
		return nil, &Error{CodeInvalid}
	}
	var out bytes.Buffer
	out.WriteString(`{"format":"` + Format + `","format_version":` + strconv.Itoa(FormatVersion) + `,"protection":"none","files":{`)
	first := true
	for _, item := range entries {
		raw, present := files[item.path]
		if !present {
			if item.optional {
				continue
			}
			return nil, &Error{CodeInvalid}
		}
		if len(raw) > cataloginput.YAMLMaxInputBytes {
			return nil, &Error{CodeLimit}
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		digest := sha256.Sum256(raw)
		out.WriteString(`"` + item.name + `":{"size":` + strconv.Itoa(len(raw)) + `,"sha256":"` + hex.EncodeToString(digest[:]) + `","content":"`)
		out.WriteString(base64.StdEncoding.EncodeToString(raw))
		out.WriteString(`"}`)
		if out.Len() > MaxBundleBytes {
			return nil, &Error{CodeLimit}
		}
	}
	out.WriteString("}}\n")
	if out.Len() > MaxBundleBytes {
		return nil, &Error{CodeLimit}
	}
	return out.Bytes(), nil
}

var (
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sizePattern   = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})$`)
)

// Decode strictly parses a bundle and returns owned bytes keyed by fixed
// application path. Format, version and protection are checked before the
// closed key set, so a newer or protected file is refused with its own code.
func Decode(raw []byte) (map[string][]byte, *Error) {
	if len(raw) > MaxBundleBytes {
		return nil, &Error{CodeLimit}
	}
	if !utf8.Valid(raw) {
		return nil, &Error{CodeInvalid}
	}
	top, ok := strictObject(raw)
	if !ok {
		return nil, &Error{CodeInvalid}
	}
	var format string
	if json.Unmarshal(top["format"], &format) != nil || format != Format {
		return nil, &Error{CodeInvalid}
	}
	version, present := top["format_version"]
	if !present || !sizePattern.Match(bytes.TrimSpace(version)) {
		return nil, &Error{CodeInvalid}
	}
	if string(bytes.TrimSpace(version)) != strconv.Itoa(FormatVersion) {
		return nil, &Error{CodeVersionUnsupported}
	}
	var protection string
	if json.Unmarshal(top["protection"], &protection) != nil || bytes.Equal(bytes.TrimSpace(top["protection"]), []byte("null")) {
		return nil, &Error{CodeInvalid}
	}
	if protection != "none" {
		return nil, &Error{CodeProtectionUnsupported}
	}
	if len(top) != 4 || top["files"] == nil {
		return nil, &Error{CodeInvalid}
	}
	listed, ok := strictObject(top["files"])
	if !ok {
		return nil, &Error{CodeInvalid}
	}
	byName := map[string]entry{}
	for _, item := range entries {
		byName[item.name] = item
	}
	for name := range listed {
		if _, known := byName[name]; !known {
			return nil, &Error{CodeInvalid}
		}
	}
	files := map[string][]byte{}
	for _, item := range entries {
		value, present := listed[item.name]
		if !present {
			if item.optional {
				continue
			}
			return nil, &Error{CodeInvalid}
		}
		content, problem := decodeFile(value)
		if problem != nil {
			return nil, problem
		}
		files[item.path] = content
	}
	return files, nil
}

func decodeFile(raw json.RawMessage) ([]byte, *Error) {
	fields, ok := strictObject(raw)
	if !ok || len(fields) != 3 || fields["size"] == nil || fields["sha256"] == nil || fields["content"] == nil {
		return nil, &Error{CodeInvalid}
	}
	sizeText := bytes.TrimSpace(fields["size"])
	if !sizePattern.Match(sizeText) {
		return nil, &Error{CodeInvalid}
	}
	size, _ := strconv.Atoi(string(sizeText))
	if size > cataloginput.YAMLMaxInputBytes {
		return nil, &Error{CodeLimit}
	}
	var digest, encoded string
	if json.Unmarshal(fields["sha256"], &digest) != nil || !digestPattern.MatchString(digest) {
		return nil, &Error{CodeInvalid}
	}
	if bytes.Equal(bytes.TrimSpace(fields["content"]), []byte("null")) || json.Unmarshal(fields["content"], &encoded) != nil {
		return nil, &Error{CodeInvalid}
	}
	if len(encoded) > maxEncoded(cataloginput.YAMLMaxInputBytes) {
		return nil, &Error{CodeLimit}
	}
	// Strict base64 still skips CR/LF; refuse them so one file has one spelling.
	if strings.ContainsAny(encoded, "\r\n") {
		return nil, &Error{CodeInvalid}
	}
	content, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, &Error{CodeInvalid}
	}
	if len(content) > cataloginput.YAMLMaxInputBytes {
		return nil, &Error{CodeLimit}
	}
	sum := sha256.Sum256(content)
	if len(content) != size || hex.EncodeToString(sum[:]) != digest {
		return nil, &Error{CodeDamaged}
	}
	return content, nil
}

// strictObject reads exactly one JSON object with unique, case-sensitive keys
// and no trailing value. Values are returned raw for the caller's typed checks.
func strictObject(raw []byte) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		name, isString := key.(string)
		if err != nil || !isString {
			return nil, false
		}
		if _, seen := fields[name]; seen {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		fields[name] = value
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return fields, true
}
