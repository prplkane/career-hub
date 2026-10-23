package joburls

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	cases := []struct{ url, identity, canonical string }{
		{" https://WWW.LinkedIn.com./comm/jobs/view/00123/?currentJobId=99#note ", "linkedin:00123", "https://www.linkedin.com/jobs/view/00123/"},
		{"https://linkedin.com/search?currentJobId=%C2%B2", "linkedin:²", "https://www.linkedin.com/jobs/view/²/"},
		{"https://linkedin.com/jobs/view/²/", "", ""},
		{"https://indeed.com/viewjob?jk=AbC_&vjk=AbC_", "indeed:abc_", "https://www.indeed.com/viewjob?jk=abc_"},
		{"https://indeed.com/viewjob?jk=AbC_&vjk=abc_", "", ""},
		{"https://indeed.com:invalid/viewjob?jk=ABCD", "indeed:abcd", "https://www.indeed.com/viewjob?jk=abcd"},
		{"https://indeed.com/viewjob?jk=&JK=++abcd++", "indeed:abcd", "https://www.indeed.com/viewjob?jk=abcd"},
		{"https://app.joinhandshake.com/ſtu/poſtings/9087/extra", "handshake:9087", "https://app.joinhandshake.com/job-search/9087"},
		{"https://linkedin.com/jobs/view/123x", "", ""},
		{"https://linkedin.com/jobs%2fview/123", "", ""},
		{"https://linkedin.com.evil.test/jobs/view/123", "", ""},
		{"https://@linkedin.com/jobs/view/123", "", ""},
		{"https://linkedin.com：443/jobs/view/123", "", ""},
		{"https://linkedin.com:℀/jobs/view/123", "", ""},
		{"https://[::1]/jobs/view/123", "", ""},
		{"httpſ://linkedin.com/jobs/view/123", "", ""},
		{"https://linkedin.com/events/123", "", ""},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			got, err := Resolve(c.url)
			if err != nil || got.Identity != c.identity || got.URL != c.canonical {
				t.Fatalf("got %+v, %v; want %q, %q", got, err, c.identity, c.canonical)
			}
		})
	}
	for _, provider := range []string{"meeboss", "12twenty"} {
		input := "https://meeboss.com/job?currentJobId=A_B~"
		id := "A_B~"
		if provider == "12twenty" {
			input, id = "https://tenant.12twenty.com/jobPostings/İıſK", "İıſK"
		}
		digest := sha256.Sum256([]byte(id))
		got, err := Resolve(input)
		if err != nil || got.Identity != provider+":sha256:"+hex.EncodeToString(digest[:]) {
			t.Fatalf("opaque identity: %+v, %v", got, err)
		}
	}
}

func TestBoundsAndFrozenProperties(t *testing.T) {
	for _, input := range []string{strings.Repeat("a", MaxURLBytes+1), string([]byte{0xff})} {
		if _, err := Resolve(input); !errors.Is(err, ErrUnsupported) || strings.Contains(err.Error(), input) {
			t.Fatalf("unsupported input did not produce a sanitized error: %v", err)
		}
	}
	if _, err := Resolve(strings.Repeat("a", MaxURLBytes)); err != nil {
		t.Fatal(err)
	}
	points := make([]byte, 0x110000)
	for point := range len(points) {
		if allDigits(string(rune(point))) {
			points[point] = 1
		}
	}
	digest := sha256.Sum256(points)
	if hex.EncodeToString(digest[:]) != "49da8d8de550119dbd4b55b07bb505e2c511143cf4a824243a85cb208dda2ef4" {
		t.Fatal("Unicode15 digit property changed")
	}
	if allDigits("") || allDigits("１２x") || !allDigits("１２²③") {
		t.Fatal("Python isdigit differs from decimal-only matching")
	}
}

func FuzzResolve(f *testing.F) {
	for _, seed := range []string{"https://linkedin.com/jobs/view/123", "https://indeed.com/?jk=abcd&vjk=ABCD", "https://tenant.12twenty.com/jobPostings/İıſK", "https://[::%indeed.com]/?jk=abcd"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, err := Resolve(input)
		if err != nil {
			return
		}
		if (got.Identity == "") != (got.URL == "") {
			t.Fatal("partial identity result")
		}
		if got.Identity != "" && !strings.HasPrefix(got.URL, "https://") {
			t.Fatal("non-canonical scheme")
		}
	})
}
