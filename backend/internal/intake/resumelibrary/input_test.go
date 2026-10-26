package resumelibrary

import (
	"fmt"
	"strings"
	"testing"
)

func TestResumeRequestBoundsAndPrivacy(t *testing.T) {
	for _, r := range []Request{
		{Operation: "unknown", Fields: map[string]any{}},
		{Operation: "list"},
		{Operation: "list", Fields: map[string]any{"search": strings.Repeat("x", maxRequestBytes+1)}},
		{Operation: "list", Fields: map[string]any{"search": "\xff"}},
		{Operation: "list", Fields: map[string]any{"search": map[string]any{"deep": "unsupported"}}},
		{Operation: "upload", Fields: map[string]any{"arbitrary_path": "private"}},
		{Operation: "archive", Fields: map[string]any{}, Content: []byte("unexpected")},
		{Operation: "upload", Fields: map[string]any{}, Content: make([]byte, MaxResumeBytes+2)},
	} {
		if p := r.validate(); p == nil {
			t.Fatal("unsupported request admitted")
		}
	}
	secret := "private-synthetic-name-and-bytes"
	for _, v := range []any{
		Request{Operation: "upload", Fields: map[string]any{"file_name": secret}, Content: []byte(secret)},
		&Error{SourceClass: secret, SourceMessage: secret},
		FileChange{RelativePath: secret, After: []byte(secret)},
		Result{Bytes: []byte(secret)},
	} {
		for _, pattern := range []string{"%v", "%+v", "%#v", "%s"} {
			if strings.Contains(fmt.Sprintf(pattern, v), secret) {
				t.Fatal("private formatter leak")
			}
		}
	}
}

func TestResumePathAndImmutableEffects(t *testing.T) {
	const id = "resume_00000000000000000000000000000001"
	base := FileChange{ResumeID: id, RelativePath: LibraryPath + "/" + id + ".pdf", AfterPresent: true, After: []byte("immutable")}
	if !ValidChange(base) {
		t.Fatal("valid immutable upload")
	}
	for _, mutate := range []func(*FileChange){
		func(c *FileChange) { c.BeforePresent = true; c.Before = []byte("old") },
		func(c *FileChange) { c.After = nil },
		func(c *FileChange) { c.After = make([]byte, MaxResumeBytes+1) },
		func(c *FileChange) { c.RelativePath = "other/" + id + ".pdf" },
		func(c *FileChange) { c.RelativePath = LibraryPath + "/nested/../" + id + ".pdf" },
		func(c *FileChange) { c.RelativePath = LibraryPath + "/" + id + ".exe" },
		func(c *FileChange) { c.ResumeID = "../" + id },
		func(c *FileChange) { c.Before = []byte("unmarked preimage") },
	} {
		c := base
		mutate(&c)
		if ValidChange(c) {
			t.Fatal("unsafe effect accepted")
		}
	}
	for _, s := range []string{"/absolute.txt", "C:/outside.txt", "applications/resumes/other.txt", LibraryPath + "/../../other.txt", LibraryPath + "/bad\x00.txt"} {
		if _, p := storedPath(s); p == nil {
			t.Fatal("unsafe path", s)
		}
	}
	v := version{id: id, path: "applications\\resumes\\library\\.\\" + id + ".pdf", fileName: "Synthetic.PDF"}
	if path, p := v.canonicalPath(); p != nil || path != base.RelativePath {
		t.Fatal("canonical Windows metadata")
	}
}

func TestResumeVerifiedBytesAndTimestampBoundaries(t *testing.T) {
	raw := []byte("synthetic")
	v := version{size: int64(len(raw)), hash: strings.ToUpper(digest(raw))}
	if p := verifyVersion(raw, &v); p != nil {
		t.Fatal("source uppercase hash supported")
	}
	for _, mutate := range []func(*version){
		func(v *version) { v.size = 0 }, func(v *version) { v.size = MaxResumeBytes + 1 },
		func(v *version) { v.hash = "invalid" }, func(v *version) { v.hash = strings.Repeat("a", 64) },
	} {
		x := v
		mutate(&x)
		if p := verifyVersion(raw, &x); p == nil {
			t.Fatal("invalid file metadata accepted")
		}
	}
	for _, s := range []string{"2026-09-09", "2026-09-09T12:00:00", "2026-09-09T07:00:00-05:00"} {
		if _, p := eventTime(s, "2026-09-09T12:00:00Z"); p != nil {
			t.Fatal("valid history timestamp")
		}
	}
	if _, p := eventTime(strings.Repeat("x", 257), "2026-09-09T12:00:00Z"); p == nil || p.Code != "RESUME_LIBRARY_TIMESTAMP_LIMIT" {
		t.Fatal("timestamp bound")
	}
	if _, p := resumeID(nil, true); p == nil || p.SourceMessage != "resume id must be a plain non-empty string" {
		t.Fatal("delete validation precedence")
	}
	key, p := optionalResume(" id ")
	if p != nil || *key != " id " {
		t.Fatal("selection identity must remain exact")
	}
}
