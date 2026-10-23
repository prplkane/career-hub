package resumelibrary

import (
	"strings"
	"testing"
)

func TestResumePublicationReferenceKeys(t *testing.T) {
	const want = LibraryPath + "/résumé.txt"
	for _, name := range []string{
		want,
		strings.ToUpper(want),
		strings.ReplaceAll(want, "/", `\`),
		LibraryPath + "/./résumé.txt",
		LibraryPath + "/nested/../résumé.txt",
		LibraryPath + "/re\u0301sume\u0301.txt",
	} {
		got, p := referenceKey(name)
		if p != nil || got != want {
			t.Fatal("supported reference spelling not grouped")
		}
	}
	for _, name := range []string{
		"", "\xff", "/" + want, "C:/" + want,
		LibraryPath + "/../../outside.txt", "other/resume.txt",
		want + ".", want + " ", want + ":stream", want + "\x00",
		LibraryPath + "/nested./../résumé.txt",
		LibraryPath + "/RESUME~1.TXT", LibraryPath + "/a?.txt",
		LibraryPath + "/NUL.txt", LibraryPath + "/com1.doc",
		LibraryPath + "/LPT¹.pdf", LibraryPath + "/conout$.txt",
		LibraryPath + "/" + strings.Repeat("a", 256),
		LibraryPath + "/a" + strings.Repeat("\u0300", 31) + ".txt",
		strings.Repeat("x", 4097),
	} {
		got, p := referenceKey(name)
		if got != "" || p == nil || p.Code != "RESUME_LIBRARY_FILE_REFERENCE_UNSUPPORTED" {
			t.Fatal("ambiguous or unbounded reference admitted")
		}
	}
}
