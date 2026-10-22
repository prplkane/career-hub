package calendarinput

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

func str(s string) cataloginput.Value   { return cataloginput.Value{Kind: "string", Text: s} }
func boolean(b bool) cataloginput.Value { return cataloginput.Value{Kind: "bool", Boolean: b} }
func mapping(entries ...cataloginput.Entry) cataloginput.Value {
	return cataloginput.Value{Kind: "mapping", Entries: entries}
}
func entry(k string, v cataloginput.Value) cataloginput.Entry {
	return cataloginput.Entry{Key: str(k), Value: v}
}

func TestNormalizeMonthBranches(t *testing.T) {
	for _, tc := range []struct{ input, want, code string }{
		{"\x1c\u00a0Sep\n2026\u3000", "2026-09", ""}, {"20٢٦-02", "20٢٦-02", ""},
		{"20٢٦", "20٢٦-01", ""}, {"Sep.20٢٦", "", FormatInvalid}, {"SEP./20٢٦", "20٢٦-09", ""},
		{"3/20٢٦", "2026-03", ""}, {"20٢٦-02-2٩", "", DateInvalid},
		{"29.2.2024Tnot-a-time", "2024-02", ""}, {"2/29/2024 anything", "2024-02", ""},
		{"2024.2/29", "2024-02", ""}, {"February 29, 2024", "2024-02", ""},
		{"29 February., 2024", "2024-02", ""}, {"February 30 2024", "", DateInvalid},
		{"0001-01-01", "0001-01", ""}, {"99991231", "9999-12", ""},
		{"2026-W01", "2025-12", ""}, {"2026W01212", "2025-12", ""},
		{"2026-01-01🛠12:30:15.123456789+00:60", "2026-01", ""},
		{"2026-01-01\x0012:30", "2026-01", ""}, {"2026-01-01T12x+01:00", "2026-01", ""},
		{"2026-01-01Z12:30", "", FormatInvalid}, {"2026-01-01T24:00", "", FormatInvalid},
		{"2026-01-01T12:00+23:60", "", FormatInvalid}, {"\t", "", ValueRequired},
		{"\xff", "", InputUnsupported},
	} {
		got, err := NormalizeMonth(str(tc.input))
		if got != tc.want || (err == nil) != (tc.code == "") || err != nil && (err.Code != tc.code || err.Field != "") {
			t.Errorf("month %q: got %q/%v, want %q/%s", tc.input, got, err, tc.want, tc.code)
		}
	}
	for _, kind := range []string{"null", "bool", "int", "float", "date", "datetime", "bytes", "sequence", "mapping"} {
		_, err := NormalizeMonth(cataloginput.Value{Kind: kind, Text: "2026-01"})
		if err == nil || err.Code != StringRequired {
			t.Fatalf("coerced %s", kind)
		}
	}
}

func TestRangeOrderAndNoMutation(t *testing.T) {
	valid := mapping(entry("start", str("2024-01")), entry("end", str("2024-02")), entry("ongoing", boolean(false)))
	before := fmt.Sprintf("%#v", valid)
	r, err := NormalizeRange(valid)
	if err != nil || r.Start != "2024-01" || r.End == nil || *r.End != "2024-02" || r.Ongoing {
		t.Fatal("valid range")
	}
	*r.End = "changed"
	if fmt.Sprintf("%#v", valid) != before {
		t.Fatal("caller input mutated")
	}
	for _, tc := range []struct {
		input       cataloginput.Value
		code, field string
	}{
		{str("2026"), RangeObjectRequired, ""},
		{mapping(entry("private_unknown", str("private"))), RangeUnknownFields, ""},
		{mapping(), StringRequired, "start"},
		{mapping(entry("start", str("")), entry("ongoing", str("true"))), ValueRequired, "start"},
		{mapping(entry("start", str("2024")), entry("ongoing", str("true")), entry("end", str("bad"))), RangeOngoingBooleanRequired, "ongoing"},
		{mapping(entry("start", str("2024")), entry("ongoing", boolean(true)), entry("end", str("bad"))), FormatInvalid, "end"},
		{mapping(entry("start", str("2024")), entry("ongoing", boolean(true)), entry("end", str("2023"))), RangeEndWithOngoing, "end"},
		{mapping(entry("start", str("2024")), entry("end", str("2023"))), RangeEndBeforeStart, "end"},
		{mapping(entry("start", str("2024")), entry("end", str(" "))), ValueRequired, "end"},
		{mapping(entry("start", str("2024")), entry("start", str("2025"))), InputUnsupported, ""},
		{mapping(cataloginput.Entry{Key: boolean(true), Value: str("x")}), InputUnsupported, ""},
	} {
		_, err := NormalizeRange(tc.input)
		if err == nil || err.Code != tc.code || err.Field != tc.field {
			t.Fatalf("range: got %#v want %s/%s", err, tc.code, tc.field)
		}
		if strings.Contains(err.Error()+err.Format("record.date_range"), "private") {
			t.Fatal("private input retained")
		}
	}
	for _, entries := range [][]cataloginput.Entry{
		{entry("start", str("2024"))},
		{entry("start", str("2024")), entry("end", str(""))},
		{entry("start", str("2024")), entry("end", cataloginput.Value{Kind: "null"})},
	} {
		r, err := NormalizeRange(mapping(entries...))
		if err != nil || r.End != nil || r.Ongoing {
			t.Fatal("optional end/default ongoing")
		}
	}
}

func TestErrorFormatting(t *testing.T) {
	for _, tc := range []struct{ code, field, want string }{
		{StringRequired, "", "date must be a string"}, {ValueRequired, "", "date is required"},
		{DateInvalid, "", "date must contain a valid calendar date"},
		{FormatInvalid, "", "date must be a supported date, timestamp, month/year, or Month YYYY"},
		{RangeObjectRequired, "", "date must be an object"}, {RangeUnknownFields, "", "date has unknown fields"},
		{RangeOngoingBooleanRequired, "ongoing", "date.ongoing must be a boolean"},
		{RangeEndWithOngoing, "end", "date.end must be empty when ongoing is true"},
		{RangeEndBeforeStart, "end", "date.end must not be earlier than date.start"},
		{"PRIVATE", "PRIVATE", "calendar input could not be validated"},
	} {
		if got := (&Error{Code: tc.code, Field: tc.field}).Format("date"); got != tc.want {
			t.Fatalf("format %q != %q", got, tc.want)
		}
	}
}

func TestISODateSemantics(t *testing.T) {
	for _, s := range []string{"00010101", "2024-02-29", "2020W53", "2020-W53-7", "20260101XX", "2026W012XX", "20260101é", "20260101\x00x"} {
		if !ValidISODate(s) {
			t.Errorf("source-valid date %q", s)
		}
	}
	for _, s := range []string{"2026-02-29", "2025-W53", "0000-01-01", "9999-W52-7", "2026-01", "2026", "2026-01-01 ", "20٢٦0101", "2026-01-01T00:00", "\xff"} {
		if ValidISODate(s) {
			t.Errorf("source-invalid date %q", s)
		}
	}
}

func TestPinnedDecimalDigits(t *testing.T) {
	h := sha256.New()
	count := 0
	var b [4]byte
	for r := rune(0); r <= 0x10ffff; r++ {
		if decimalDigit(r) >= 0 {
			count++
			binary.BigEndian.PutUint32(b[:], uint32(r))
			h.Write(b[:])
		}
	}
	if count != 680 || fmt.Sprintf("%x", h.Sum(nil)) != "93a69f3604c97cdaa6460b7acf761f7edd20725f281233cd2a52a9dcfa38a312" {
		t.Fatal("UCD15 decimal drift")
	}
	if decimalNumber("bad") != -1 || !reflect.DeepEqual([]int{decimalDigit('٢'), decimalDigit('𝟞')}, []int{2, 6}) {
		t.Fatal("decimal conversion")
	}
}

func TestAwareISODateTime(t *testing.T) {
	for _, text := range []string{
		"2026-09-08T12Z", "20260908T12+00", "2026-W37-2X12Z",
		"2026-09-08😀12+01:00", "2026-09-08T12x+01:00",
		"2026-09-08T12\x00Z", "2026-09-08T12Z\x00ignored",
		"2026-09-08T12+00:00:00.999999", "2026-09-08T12:00\x00+00:00",
	} {
		if !ValidAwareISODateTime(text) {
			t.Errorf("source-aware timestamp %q", text)
		}
	}
	for _, text := range []string{
		"", "2026-09-08", "2026-09-08T12:00", "2026-09-08+12",
		"2026-09-08Z12", "2026-09-08T12+23:59:60", "2026-09-08T24Z",
		"2026-09-08T12Ztail", "2026-09-08\xff12Z", "2026-02-29T12Z",
	} {
		if ValidAwareISODateTime(text) {
			t.Errorf("not a valid aware timestamp %q", text)
		}
	}
}

func FuzzNormalizeMonth(f *testing.F) {
	for _, text := range []string{"2026-01", "2026-W01", "2026-01-01T12.123456+00:60", "\x00", "20٢٦-01"} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 4096 {
			t.Skip()
		}
		_, _ = NormalizeMonth(str(text))
		_ = ValidISODate(text)
		_ = ValidAwareISODateTime(text)
	})
}
