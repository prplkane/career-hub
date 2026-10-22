package resumelibrary

import (
	"context"
	"database/sql"
	"encoding/json"
	"path"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/text/unicode/norm"
)

// Witness is private exact-row recovery evidence. SQLite atomically commits the
// version with import/selection changes; recovery settles files, never replays SQL.
type Witness struct {
	ResumeID string `json:"resume_id"`
	Present  bool   `json:"present"`
	SHA256   string `json:"sha256"`
}

func MutationWitness(ctx context.Context, tx *sql.Tx, id string) (Witness, *Error) {
	w := Witness{ResumeID: id}
	if ctx == nil || tx == nil || len(id) > 4096 || !utf8.ValidString(id) {
		return w, unsupported("WITNESS_UNSUPPORTED")
	}
	e := engine{ctx: ctx, tx: tx, remaining: maxScanBytes}
	row, p := e.one("SELECT "+textColumns("resume_id", "display_name", "relative_path", "file_name", "sha256", "added_at", "archived_at", "origin")+","+intSQL("size_bytes")+" FROM resume_library WHERE resume_id=?", id)
	if p != nil {
		return w, p
	}
	imports, p := e.query("SELECT "+textColumns("source_id", "source_sha256", "created_at")+" FROM approved_import_resume_versions WHERE resume_id=? ORDER BY source_id", 1, id)
	if p != nil {
		return w, p
	}
	if row == nil {
		if len(imports) != 0 {
			return w, unsupported("WITNESS_UNSUPPORTED")
		}
		return w, nil
	}
	raw, err := json.Marshal([]any{"resume-row-v1", row, imports})
	if err != nil {
		return w, unsupported("WITNESS_UNSUPPORTED")
	}
	w.Present = true
	w.SHA256 = digest(raw)
	return w, nil
}

func ValidChange(c FileChange) bool {
	if len(c.ResumeID) > 4096 || !utf8.ValidString(c.ResumeID) || !plainName(c.ResumeID) || c.ResumeID == ".." || !utf8.ValidString(c.RelativePath) || len(c.RelativePath) > 4096 {
		return false
	}
	if c.BeforePresent == c.AfterPresent || len(c.Before) > MaxResumeBytes || len(c.After) > MaxResumeBytes {
		return false
	}
	if c.BeforePresent && len(c.Before) == 0 || !c.BeforePresent && len(c.Before) != 0 || c.AfterPresent && len(c.After) == 0 || !c.AfterPresent && len(c.After) != 0 {
		return false
	}
	for _, s := range []string{".pdf", ".doc", ".docx", ".rtf", ".txt"} {
		if c.RelativePath == LibraryPath+"/"+c.ResumeID+s {
			return true
		}
	}
	return false
}

// CheckPublication verifies exact immutable metadata and refuses every other
// library/attachment path alias, including legacy NULL-ID attachments. An ID
// guard alone is insufficient authority to delete a physical file.
func CheckPublication(ctx context.Context, tx *sql.Tx, c FileChange, committed bool) *Error {
	if ctx == nil || tx == nil || !ValidChange(c) {
		return unsupported("PUBLICATION_UNSUPPORTED")
	}
	e := engine{ctx: ctx, tx: tx, remaining: maxScanBytes}
	v, p := e.version(c.ResumeID)
	if p != nil {
		return p
	}
	wantPresent := c.BeforePresent
	if committed {
		wantPresent = c.AfterPresent
	}
	if (v != nil) != wantPresent {
		return unsupported("DATABASE_STATE_CHANGED")
	}
	if v != nil {
		got, p := v.canonicalPath()
		if p != nil || got != c.RelativePath {
			return unsupported("PUBLICATION_METADATA_CHANGED")
		}
		content := c.Before
		if committed {
			content = c.After
		}
		if p = verifyVersion(content, v); p != nil {
			return unsupported("PUBLICATION_METADATA_CHANGED")
		}
	}
	want, p := referenceKey(c.RelativePath)
	if p != nil {
		return p
	}
	for _, query := range []string{
		"SELECT " + textColumns("resume_id", "relative_path") + " FROM resume_library ORDER BY resume_id",
		"SELECT " + textColumns("resume_id", "relative_path") + " FROM application_resume_attachments ORDER BY job_id",
	} {
		rows, p := e.query(query, 10000)
		if p != nil {
			return p
		}
		for _, r := range rows {
			name, ok := r[1].(string)
			if !ok {
				return unsupported("DATABASE_VALUE_UNSUPPORTED")
			}
			key, p := referenceKey(name)
			if p != nil {
				return p
			}
			if key == want && (!wantPresent || r[0] != c.ResumeID) {
				return unsupported("FILE_REFERENCE_CONFLICT")
			}
		}
	}
	return nil
}

// Foreign legacy references are not permission to reinterpret Windows aliases.
// Dot/space trimming, alternate streams and 8.3 names can reference the very
// same bytes while their textual keys differ. We cannot prove non-aliasing for
// those spellings, so refuse publication instead of silently discarding them.
// This is reference classification, not a second filesystem resolver.
func referenceKey(raw string) (string, *Error) {
	bad := func() (string, *Error) { return "", unsupported("FILE_REFERENCE_UNSUPPORTED") }
	if !utf8.ValidString(raw) || len(raw) > 4096 || raw == "" {
		return bad()
	}
	name := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(name, "/") {
		return bad()
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return bad()
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			continue
		}
		if len(part) > 255 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `<>:"|?*~`) {
			return bad()
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		switch base {
		case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$":
			return bad()
		}
		if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
			tail := base[3:]
			if len(tail) == 1 && tail[0] >= '1' && tail[0] <= '9' || tail == "¹" || tail == "²" || tail == "³" {
				return bad()
			}
		}
	}
	clean := path.Clean(name)
	normalized := norm.NFC.String(clean)
	if strings.Count(clean, "\u034f") != strings.Count(normalized, "\u034f") {
		return bad()
	}
	key := textcompat.Fold(normalized)
	if !strings.HasPrefix(key, LibraryPath+"/") {
		return bad()
	}
	return key, nil
}
