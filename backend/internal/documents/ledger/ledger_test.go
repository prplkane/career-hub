package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"careerhub.local/backend/internal/documents/envelope"
)

func sample() map[string]any {
	return map[string]any{
		"id": "doc_" + strings.Repeat("a", 32), "title": " Synthetic document ",
		"category": " Identity ", "issued_on": "2024-02-29", "expires_on": "2026-09-08",
		"notes": "Private synthetic notes\n\x00", "filename": "synthetic.pdf", "content_type": "application/pdf",
		"size_bytes": 123, "sha256": strings.Repeat("b", 64),
		"created_at": "2026-09-08T12:00:00+00:00", "updated_at": "2026-09-08T12Z", "archived": false,
	}
}

func encoded(records ...map[string]any) []byte {
	if records == nil {
		records = []map[string]any{}
	}
	value, e := json.Marshal(map[string]any{"version": 1, "documents": records})
	if e != nil {
		panic("invalid synthetic fixture")
	}
	return value
}

type ledgerCase struct {
	Name        string
	Raw         []byte
	Code, Delta string
}

func ledgerCases() []ledgerCase {
	cases := []ledgerCase{{Name: "empty", Raw: encoded()}, {Name: "one", Raw: encoded(sample())}}
	add := func(name string, raw []byte, code string) {
		cases = append(cases, ledgerCase{Name: name, Raw: raw, Code: code})
	}
	field := func(name string, value any, ok bool) {
		item := sample()
		item[name] = value
		code := CodeInvalid
		if ok {
			code = ""
		}
		add(fmt.Sprintf("field-%s-%d", name, len(cases)), encoded(item), code)
	}
	for _, name := range []string{"id", "title", "category", "issued_on", "expires_on", "notes", "filename", "content_type", "size_bytes", "sha256", "created_at", "updated_at", "archived"} {
		item := sample()
		delete(item, name)
		add("missing-"+name, encoded(item), CodeInvalid)
		for _, value := range []any{nil, true, 1.25, []any{}, map[string]any{}} {
			// Source has an unhandled TypeError for an otherwise valid record's
			// unhashable MIME. Keep those named deltas separate below.
			if name == "archived" {
				if boolean, ok := value.(bool); ok && boolean {
					continue
				}
			}
			if name == "content_type" {
				switch value.(type) {
				case []any, map[string]any:
					continue
				}
			}
			field(name, value, false)
		}
	}
	for _, name := range []string{"id", "sha256"} {
		for _, value := range []string{"", "doc_a", strings.Repeat("A", 64), strings.Repeat("a", 65), "doc_" + strings.Repeat("a", 32) + "\n"} {
			field(name, value, false)
		}
	}
	for _, entry := range []struct {
		name  string
		limit int
		empty bool
	}{
		{"title", 500, false}, {"category", 120, false}, {"notes", 5000, true}, {"filename", 255, false},
	} {
		field(entry.name, "", entry.empty)
		field(entry.name, strings.Repeat("😀", entry.limit), true)
		field(entry.name, strings.Repeat("😀", entry.limit+1), false)
		field(entry.name, "   ", true)
	}
	for _, name := range []string{"issued_on", "expires_on"} {
		for _, date := range []string{"", "2026-02-29", "2026-13-01", "2026-09-00", "20260908", "2026-W37-2", "2026-09-08 ", "2026-09-08\n", "٢٠٢٦-09-08"} {
			field(name, date, date == "")
		}
	}
	for _, pair := range [][2]string{{"0001-01-01", "9999-12-31"}, {"2026-09-08", "2026-09-08"}, {"2026-09-08", "2024-02-29"}} {
		item := sample()
		item["issued_on"], item["expires_on"] = pair[0], pair[1]
		code := ""
		if pair[1] < pair[0] {
			code = CodeInvalid
		}
		add("date-range", encoded(item), code)
	}
	for _, name := range []string{"created_at", "updated_at"} {
		for _, stamp := range []string{"20260908T12+00", "2026-W37-2😀12Z", "2026-09-08T12x+01:00", "2026-09-08T12\x00Z", "2026-09-08T12Z\x00ignored", "2026-09-08T12+00:00:00.999999"} {
			field(name, stamp, true)
		}
		for _, stamp := range []string{"", "2026-09-08", "2026-09-08T12:00", "2026-09-08T12+24", "2026-09-08T24Z", "2026-09-08T12Ztail"} {
			field(name, stamp, false)
		}
		prefix := "2026-09-08T12."
		field(name, prefix+strings.Repeat("1", 64-len(prefix)-1)+"Z", true)
		field(name, prefix+strings.Repeat("1", 65-len(prefix)-1)+"Z", false)
	}
	for _, name := range []string{"document.pdf", "CON", "NUL.pdf", "a.", " a ", "..", ":", "a\x7f.pdf", "hello\u2028.pdf"} {
		field("filename", name, true)
	}
	for _, name := range []string{".", "a/b", "a/", "../a", "a\x00.pdf", "a\n.pdf"} {
		field("filename", name, false)
	}
	for _, name := range []string{"folder\\a.pdf", "C:a.pdf", "C:", "é:document.pdf", "😀:document.pdf"} {
		field("filename", name, runtime.GOOS != "windows")
	}
	for _, value := range []any{0, -1, MaxDocumentBytes + 1, "123"} {
		field("size_bytes", value, false)
	}
	field("size_bytes", 1, true)
	field("size_bytes", MaxDocumentBytes, true)
	field("content_type", "image/png", true)
	field("content_type", "image/jpeg", true)
	field("content_type", "image/jpg", false)
	field("archived", true, true)
	item := sample()
	item["unknown"] = "private"
	add("unknown-field", encoded(item), CodeInvalid)
	add("duplicate-id", encoded(sample(), sample()), CodeInvalid)
	for _, raw := range []string{"", "null", "[]", `{"version":1}`, `{"version":1,"documents":{}}`, `{"version":1.0,"documents":[]}`, `{"version":true,"documents":[]}`, `{"version":1e0,"documents":[]}`, `{"version":2,"documents":[]}`, `{"version":1,"documents":[],"extra":0}`, `{"version":1,"version":1,"documents":[]}`, `{"version":1,"documents":[],"docum\u0065nts":[]}`, `{"version":1,"documents":[null]}`, `{"version":1,"documents":[]}null`} {
		add("invalid-root", []byte(raw), CodeInvalid)
	}
	base := encoded(sample())
	add("duplicate-title", bytes.Replace(base, []byte(`"title":`), []byte(`"title":"other","title":`), 1), CodeInvalid)
	add("wrong-case-title", bytes.Replace(base, []byte(`"title":`), []byte(`"Title":`), 1), CodeInvalid)
	add("duplicate-nested-invalid-field", bytes.Replace(base, []byte(`"size_bytes":123`), []byte(`"size_bytes":{"x":1,"x":2}`), 1), CodeInvalid)
	add("float-size", bytes.Replace(base, []byte(`"size_bytes":123`), []byte(`"size_bytes":123.0`), 1), CodeInvalid)
	add("NaN-size", bytes.Replace(base, []byte(`"size_bytes":123`), []byte(`"size_bytes":NaN`), 1), CodeInvalid)
	add("huge-int-size", bytes.Replace(base, []byte(`"size_bytes":123`), []byte(`"size_bytes":99999999999999999999`), 1), CodeInvalid)
	add("bad-utf8", append([]byte{0xff}, base...), CodeInvalid)
	add("UTF8-BOM", append([]byte{0xef, 0xbb, 0xbf}, base...), CodeInvalid)
	add("noncanonical-spacing", append([]byte(" \t\r\n"), base...), "")
	add("escaped-field-name", bytes.Replace(base, []byte(`"title":`), []byte(`"titl\u0065":`), 1), "")
	for _, tc := range []struct{ value, delta, code string }{
		{`"\ud83d\ude00"`, "", ""}, {`"\\ud800"`, "", ""}, {`"\ufffd"`, "", ""},
		{`"\ud800"`, "scalar-surrogate", CodeUnsupported}, {`"\udfff"`, "scalar-surrogate", CodeUnsupported},
		{`"\ud800\u1234"`, "scalar-surrogate", CodeUnsupported},
	} {
		raw := bytes.Replace(base, []byte(`" Synthetic document "`), []byte(tc.value), 1)
		cases = append(cases, ledgerCase{Name: "escaped-title", Raw: raw, Code: tc.code, Delta: tc.delta})
	}
	for _, value := range []any{[]any{}, map[string]any{}} {
		item := sample()
		item["content_type"] = value
		cases = append(cases, ledgerCase{Name: "unhashable-mime", Raw: encoded(item), Code: CodeInvalid, Delta: "unhashable-mime"})
	}
	// Source set sort has unspecified ordering only within equal casefold keys.
	records := []map[string]any{}
	for i, title := range []string{" Zed ", "alpha", "Alpha", "STRASSE", "Straße", "\x1cAlpha\u0085", "", "\u2003\u2003", "hidden"} {
		item := sample()
		item["id"] = fmt.Sprintf("doc_%032x", i)
		item["title"] = title
		if title == "" {
			item["title"] = " \n "
		}
		if title == "hidden" {
			item["archived"] = true
		}
		records = append(records, item)
	}
	add("title-projection", encoded(records...), "")
	return cases
}

func TestLedgerSchema(t *testing.T) {
	for i, tc := range ledgerCases() {
		t.Run(fmt.Sprintf("%03d-%s", i, tc.Name), func(t *testing.T) {
			before := bytes.Clone(tc.Raw)
			documents, e := Parse(tc.Raw)
			if !bytes.Equal(before, tc.Raw) {
				t.Fatal("input mutated")
			}
			code := ""
			if e != nil {
				code = e.Code
			}
			if code != tc.Code {
				t.Fatalf("got %s, want %s", code, tc.Code)
			}
			if e != nil && documents != nil {
				t.Fatal("partial documents escaped")
			}
			if e == nil && documents == nil {
				t.Fatal("valid empty ledger must be []")
			}
		})
	}
}

func TestLedgerByteBoundsAndOwnership(t *testing.T) {
	base := encoded(sample())
	limit := append(bytes.Clone(base), bytes.Repeat([]byte(" "), envelope.MaxObjectBytes-len(base))...)
	if _, e := Parse(limit); e != nil {
		t.Fatal("exact plaintext byte limit rejected")
	}
	if docs, e := Parse(append(limit, ' ')); e == nil || docs != nil {
		t.Fatal("oversize plaintext admitted")
	}
	docs, e := Parse(base)
	if e != nil {
		t.Fatal(e)
	}
	for i := range base {
		base[i] = 'x'
	}
	if docs[0].Title != " Synthetic document " || docs[0].Notes != "Private synthetic notes\n\x00" {
		t.Fatal("stored values changed or aliased")
	}
	all := Metadata(docs, []byte("one"), true)
	active := Metadata(docs, []byte("two"), false)
	if all.Revision == active.Revision {
		t.Fatal("revision ignored encrypted bytes")
	}
	all.Documents[0].Title = "mutated"
	if docs[0].Title == "mutated" || active.Documents[0].Title == "mutated" {
		t.Fatal("listing aliases input/other listing")
	}
	if Metadata(nil, nil, true).Revision != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatal("missing ledger revision")
	}
}

func TestTitleProjectionPrivacy(t *testing.T) {
	documents := []Document{{Title: " Alpha ", Notes: "private notes", Filename: "secret.pdf"}, {Title: "alpha"}, {Title: "Alpha"}, {Title: "hidden", Archived: true}, {Title: "\x1c \u0085"}}
	if got := Titles(documents); !reflect.DeepEqual(got, []string{"Alpha", "alpha"}) {
		t.Fatal("wrong title membership/order")
	}
	value, e := json.Marshal(Titles(documents))
	if e != nil || bytes.Contains(value, []byte("secret")) || bytes.Contains(value, []byte("notes")) {
		t.Fatal("metadata leaked into model projection")
	}
	if Titles(nil) == nil || Metadata(nil, nil, false).Documents == nil {
		t.Fatal("null projection")
	}
	for _, e := range []*Error{{Code: CodeInvalid}, {Code: CodeUnsupported}, {Code: "PRIVATE"}, nil} {
		if strings.Contains(fmt.Sprintf("%+v %#v %s", e, e, e), "PRIVATE") {
			t.Fatal("diagnostic formatting leaked contents")
		}
	}
}

func TestFilenamePlatformContract(t *testing.T) {
	for _, text := range []string{"folder\\name", "é:name", "😀:name", "C:name"} {
		if metadataFilename(text, true) || !metadataFilename(text, false) {
			t.Fatal("metadata platform contract changed")
		}
	}
}

func FuzzLedgerParse(f *testing.F) {
	for _, raw := range [][]byte{encoded(), encoded(sample()), []byte("null"), []byte(`{"version":1,"documents":[]}`)} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		docs, e := Parse(raw)
		if e != nil {
			if docs != nil {
				t.Fatal("partial result")
			}
			return
		}
		if docs == nil {
			t.Fatal("null result")
		}
		_ = Titles(docs)
		_ = Metadata(docs, raw, false)
	})
}
