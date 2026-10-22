package calendarinput

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

func rangeOf(start, end string, ongoing bool) cataloginput.Value {
	return mapping(entry("start", str(start)), entry("end", str(end)), entry("ongoing", boolean(ongoing)))
}

func TestRangeFieldsLegacyAndDisplay(t *testing.T) {
	for _, tc := range []struct {
		text, start, end string
		ongoing          bool
	}{
		{"Jan 2024 - Jun 2025", "2024-01", "2025-06", false},
		{"Dec - Jan 2025", "2024-12", "2025-01", false},
		{"Dec 2024 - Jan", "2024-12", "2025-01", false},
		{"Jan - Jan 2025", "2025-01", "2025-01", false},
		{"Jan 2024 - Jan", "2024-01", "2024-01", false},
		{"Jan 2024—Preſent", "2024-01", "", true},
		{"Jan 2024 TO CURRENT", "2024-01", "", true},
		{"Jan 2024 – now", "2024-01", "", true},
		{"2024-02", "2024-02", "", false},
		{"12. - Jan 2025", "2024-12", "2025-01", false},
		{"Jan 20٢٥ - 12", "", "", false}, // Source's lexical ordering rejects this range.
		{"Dec 2099 - Jan", "2099-12", "2100-01", false},
		{"Dec - Jan 1900", "1899-12", "1900-01", false},
		{"during university", "", "", false},
		{"Jan - Present", "", "", false},
		{"Jan - Jun", "", "", false},
		{"2025 - 2024", "", "", false},
		{"2024-2025", "", "", false},
		{"2024 - 2025 - 2026", "", "", false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, e := NormalizeRangeFields(str(" \x1c"+tc.text+"\u00a0"), cataloginput.Value{}, true)
			if e != nil || got.Dates != tc.text {
				t.Fatal("legacy display changed or rejected")
			}
			if tc.start == "" {
				if got.DateRange != nil {
					t.Fatal("unrecognized range was invented")
				}
				return
			}
			want := Range{Start: tc.start, Ongoing: tc.ongoing}
			if tc.end != "" {
				want.End = &tc.end
			}
			if !reflect.DeepEqual(got.DateRange, &want) {
				t.Fatalf("range = %+v", got.DateRange)
			}
		})
	}
	for _, tc := range []struct {
		span    cataloginput.Value
		display string
	}{
		{rangeOf("2024-01", "2025-06", false), "Jan 2024 - Jun 2025"},
		{rangeOf("20٢٤-01", "", true), "Jan 2024 - Present"},
		{rangeOf("Feb 2024", "", false), "Feb 2024"},
	} {
		before, _ := json.Marshal(tc.span)
		got, e := NormalizeRangeFields(str("opaque old dates"), tc.span, true)
		after, _ := json.Marshal(tc.span)
		if e != nil || got.Dates != tc.display || !reflect.DeepEqual(before, after) {
			t.Fatal("structured display, precedence, or input ownership failed")
		}
		owned := got.DateRange.Value()
		owned.Entries[0].Value.Text = "changed"
		if got.DateRange.Start == "changed" {
			t.Fatal("range value aliases its source")
		}
	}
}

func TestRangeFieldsErrorOrderAndAbsence(t *testing.T) {
	for _, tc := range []struct {
		dates, span cataloginput.Value
		required    bool
		message     string
	}{
		{boolean(false), str("bad"), true, "dates must be a string"},
		{str(""), rangeOf("", "bad", true), true, "date_range.start is required"},
		{str(""), str("bad"), true, "date_range must be an object"},
		{str(" "), cataloginput.Value{}, true, "dates is required"},
		{str(""), rangeOf("2500-01-01", "", false), true, "date_range.start must be a supported date, timestamp, month/year, or Month YYYY"},
		{str(""), mapping(entry("private-key", str("private"))), true, "date_range has unknown fields"},
	} {
		_, e := NormalizeRangeFields(tc.dates, tc.span, tc.required)
		if e == nil || e.Message() != tc.message {
			t.Fatalf("unexpected error for synthetic case: %v", e)
		}
		if strings.Contains(fmt.Sprintf("%+v", e), "private") {
			t.Fatal("private field leaked")
		}
	}
	for _, v := range []cataloginput.Value{{}, {Kind: "null"}, str(""), str("\u00a0 ")} {
		got, e := NormalizeRangeFields(v, cataloginput.Value{}, false)
		if e != nil || got.Dates != "" || got.DateRange != nil {
			t.Fatal("optional clear retained metadata")
		}
	}
	_, e := NormalizeRangeFields(str(string([]byte{0xff})), cataloginput.Value{}, true)
	if e == nil || e.Cause.Code != InputUnsupported {
		t.Fatal("invalid UTF-8 accepted")
	}
	if standaloneMonth("23") != 0 || standaloneMonth("NotAMonth") != 0 {
		t.Fatal("invalid standalone month")
	}
	end := "2025-01"
	_, formatError := formatRange(Range{Start: "2024-01", Ongoing: true, End: &end})
	if formatError == nil {
		t.Fatal("format validation bypassed")
	}
	if _, monthError := formatMonth("unknown"); monthError == nil {
		t.Fatal("month validation bypassed")
	}
	large := strings.Repeat(" ", 100000) + "Jan 2024 - Jun 2024"
	if got, e := NormalizeRangeFields(str(large), cataloginput.Value{}, true); e != nil || got.DateRange == nil {
		t.Fatal("invented small date-text limit")
	}
}
