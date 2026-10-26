package publicjobresolver

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestPinnedExtractionContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/extraction_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Reference string
		Cases     []struct {
			Body          []byte
			Charset       string
			BytesRead     int `json:"bytes_read"`
			RedirectCount int `json:"redirect_count"`
			Expected      struct {
				Title    string `json:"expected_title"`
				Company  string `json:"expected_company"`
				Location string `json:"expected_location"`
			}
			Output      *Extraction
			Code, Stage string
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, c := range fixture.Cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			got, e := Extract(&HTMLResponse{Body: c.Body, Charset: c.Charset, BytesRead: c.BytesRead, RedirectCount: c.RedirectCount}, Expected{c.Expected.Title, c.Expected.Company, c.Expected.Location})
			if c.Code != "" {
				if e == nil || e.Code != c.Code || e.Stage != c.Stage {
					t.Fatalf("%v want %s/%s", e, c.Code, c.Stage)
				}
				return
			}
			if e != nil || !reflect.DeepEqual(got, c.Output) {
				t.Fatalf("got %+v / %v\nwant %+v", got, e, c.Output)
			}
		})
	}
}
func TestPinnedRedirectAndCharset(t *testing.T) {
	var joins []struct{ Base, Location, Output string }
	raw, err := os.ReadFile("testdata/redirect_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &joins) != nil {
		t.Fatal("bad joins")
	}
	for _, v := range joins {
		got, err := joinRedirect(v.Base, v.Location)
		if err != nil || got != v.Output {
			t.Errorf("join %q: %q %v want %q", v.Location, got, err, v.Output)
		}
	}
	var charsets []struct {
		Body            []byte
		Charset, Output string
	}
	raw, err = os.ReadFile("testdata/charset_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &charsets) != nil {
		t.Fatal("bad charsets")
	}
	for _, v := range charsets {
		got, err := decodeHTML(v.Body, v.Charset)
		if err != nil || got != v.Output {
			t.Errorf("decode %x: %q %v want %q", v.Body, got, err, v.Output)
		}
	}
}
func TestExtractionLimits(t *testing.T) {
	for _, body := range []string{strings.Repeat("<div>", 50001), strings.Repeat("<div itemprop=description>", 181), "<script type=application/ld+json>" + strings.Repeat("x", 1000001) + "</script>", "<script type=application/ld+json>[" + strings.Repeat("0,", 20000) + "0]</script>"} {
		_, e := Extract(&HTMLResponse{Body: []byte(body), BytesRead: len(body), Charset: "utf-8"}, Expected{})
		if e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_CONTENT_UNSUPPORTED" {
			t.Fatal(e)
		}
	}
}
