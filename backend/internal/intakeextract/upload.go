package intakeextract

import (
	"archive/zip"
	"bytes"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const MaxUploadBytes = 10 << 20

// ValidateUpload mirrors the intake upload envelope, not complete document
// extraction. It never parses XML, extracts ZIP entries or normalizes contents.
func ValidateUpload(encodedName string, content []byte) (string, error) {
	name, e := url.PathUnescape(encodedName)
	if e != nil || name == "" || !utf8.ValidString(name) || textcompat.Strip(name) != name || path.Base(name) != name || strings.ContainsAny(name, "/\\\x00:") || len(name) > 255 {
		return "", ErrInput
	}
	if len(content) == 0 || len(content) > MaxUploadBytes {
		return "", ErrLimit
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".txt":
		if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			return "", ErrInput
		}
	case ".docx":
		archive, e := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if e != nil {
			return "", ErrInput
		}
		if len(archive.File) > 2048 {
			return "", ErrLimit
		}
		var expanded uint64
		foundContent, foundDocument := false, false
		for _, file := range archive.File {
			if file.UncompressedSize64 > (50<<20)-expanded {
				return "", ErrLimit
			}
			expanded += file.UncompressedSize64
			foundContent = foundContent || file.Name == "[Content_Types].xml"
			foundDocument = foundDocument || file.Name == "word/document.xml"
		}
		if !foundContent || !foundDocument {
			return "", ErrInput
		}
	default:
		return "", ErrInput
	}
	return name, nil
}
