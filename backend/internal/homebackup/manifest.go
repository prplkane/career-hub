// Package homebackup validates local recovery archives. Filesystem publication
// and restore ownership are separate; a valid archive is not permission to write.
package homebackup

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/textcompat"
)

const (
	FormatVersion          = 2
	ManifestName           = "manifest.json"
	MaxFiles               = 25000
	MaxManifestBytes       = 8 << 20
	MaxEntryBytes    int64 = 2 << 30
	MaxTotalBytes    int64 = 32 << 30
)

type Error struct{ Code string }

func (e *Error) Error() string   { return e.Code }
func failure(code string) *Error { return &Error{Code: code} }

type File struct {
	RelativePath string `json:"relative_path"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
}
type Manifest struct {
	FormatVersion         int    `json:"format_version"`
	DataHomeFormatVersion int    `json:"data_home_format_version"`
	DataHomeID            string `json:"data_home_id"`
	DataHomeChannel       string `json:"data_home_channel"`
	SQLiteSchemaVersion   *int64 `json:"sqlite_schema_version"`
	Files                 []File `json:"files"`
}

func allowedRoot(name string) bool {
	switch name {
	case "applications", "career_vault", "documents", "jobs", "outputs", "resume_versions", "templates", "logs":
		return true
	}
	return false
}

// Names are retained exactly, never normalized into a different ZIP entry.
// This preserves the Python inspection boundary; managed restore must separately
// reject platform aliases/device names before creating any destination files.
func safeName(name string) bool {
	if name == "" || !utf8.ValidString(name) || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func exact(fields map[string]any, names ...string) bool {
	if len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	return true
}
func integer(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(number), 10, 64)
	return n, err == nil
}
func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func ParseManifest(raw []byte) (Manifest, *Error) {
	var out Manifest
	if len(raw) > MaxManifestBytes || !validJSONScalars(raw) {
		return out, failure("BACKUP_MANIFEST_INVALID")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var fields map[string]any
	if d.Decode(&fields) != nil || !exact(fields, "format_version", "data_home_format_version", "data_home_id", "data_home_channel", "sqlite_schema_version", "files") {
		return out, failure("BACKUP_MANIFEST_INVALID")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return out, failure("BACKUP_MANIFEST_INVALID")
	}
	format, ok := fields["format_version"].(json.Number)
	f, err := strconv.ParseFloat(string(format), 64)
	if !ok || err != nil || f != FormatVersion {
		return out, failure("BACKUP_FORMAT_UNSUPPORTED")
	}
	version, ok := integer(fields["data_home_format_version"])
	if !ok || version != 2 {
		return out, failure("BACKUP_HOME_FORMAT_UNSUPPORTED")
	}
	id, idOK := fields["data_home_id"].(string)
	channel, channelOK := fields["data_home_channel"].(string)
	if !idOK || !channelOK {
		return out, failure("BACKUP_MANIFEST_INVALID")
	}
	if _, problem := datahome.MarkerBytes(channel, id); problem != nil {
		return out, failure("BACKUP_MANIFEST_INVALID")
	}
	var schema *int64
	if fields["sqlite_schema_version"] != nil {
		n, ok := integer(fields["sqlite_schema_version"])
		if !ok || n > applications.SchemaVersion {
			return out, failure("BACKUP_SCHEMA_UNSUPPORTED")
		}
		schema = &n
	}
	items, ok := fields["files"].([]any)
	if !ok || len(items) > MaxFiles {
		return out, failure("BACKUP_MANIFEST_INVALID")
	}
	files := make([]File, 0, len(items))
	seen := make(map[string]bool, len(items))
	var total int64
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok || !exact(item, "relative_path", "size_bytes", "sha256") {
			return out, failure("BACKUP_MANIFEST_INVALID")
		}
		name, nameOK := item["relative_path"].(string)
		digest, digestOK := item["sha256"].(string)
		size, sizeOK := integer(item["size_bytes"])
		if !nameOK || !safeName(name) || !allowedRoot(strings.SplitN(name, "/", 2)[0]) {
			return out, failure("BACKUP_PATH_INVALID")
		}
		if !sizeOK || size < 0 || size > MaxEntryBytes || !digestOK || !lowerHex(digest, 64) {
			return out, failure("BACKUP_MANIFEST_INVALID")
		}
		total += size
		if total > MaxTotalBytes {
			return out, failure("BACKUP_SIZE_LIMIT")
		}
		key := textcompat.Fold(name)
		if seen[key] {
			return out, failure("BACKUP_PATH_COLLISION")
		}
		seen[key] = true
		files = append(files, File{name, size, digest})
	}
	return Manifest{FormatVersion, 2, id, channel, schema, files}, nil
}

// encoding/json replaces unpaired UTF-16 escapes. Refuse those instead of
// interpreting a different path; bound nesting before allocating decoded maps.
func validJSONScalars(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	depth := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			for i++; i < len(raw); i++ {
				if raw[i] == '"' {
					break
				}
				if raw[i] != '\\' {
					continue
				}
				i++
				if i >= len(raw) {
					return false
				}
				if raw[i] != 'u' {
					continue
				}
				if i+4 >= len(raw) {
					return false
				}
				n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
				if err != nil {
					return false
				}
				i += 4
				if n >= 0xdc00 && n <= 0xdfff {
					return false
				}
				if n >= 0xd800 && n <= 0xdbff {
					if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
						return false
					}
					low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
					if err != nil || low < 0xdc00 || low > 0xdfff {
						return false
					}
					i += 6
				}
			}
		} else if raw[i] == '{' || raw[i] == '[' {
			depth++
			if depth > 8 {
				return false
			}
		} else if raw[i] == '}' || raw[i] == ']' {
			depth--
		}
	}
	return true
}
