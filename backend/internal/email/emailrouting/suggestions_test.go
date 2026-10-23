package emailrouting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestUnknownSenderSuggestionsMatchPythonSource(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "migration_email_sender_suggestion_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name          string   `json:"name"`
			SenderAddress string   `json:"sender_address"`
			MessageDates  []string `json:"message_dates"`
			Signals       struct {
				Job         int `json:"job"`
				Application int `json:"application"`
				Service     int `json:"service"`
			} `json:"signals"`
			Want SenderSuggestion `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) < 10 {
		t.Fatalf("invalid shared fixture: %v", err)
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			if len(test.MessageDates) == 0 {
				t.Fatal("pending sender needs at least one receipt")
			}
			days := map[string]bool{}
			for _, value := range test.MessageDates {
				if value != "" {
					days[value] = true
				}
			}
			ordered := make([]string, 0, len(days))
			for value := range days {
				ordered = append(ordered, value)
			}
			slices.Sort(ordered)
			first, last := "", ""
			if len(ordered) > 0 {
				first, last = ordered[0], ordered[len(ordered)-1]
			}
			got := SuggestUnknownSender(UnknownSenderSignals{
				SenderAddress: test.SenderAddress, MessageCount: len(test.MessageDates),
				DistinctMessageDays: len(days), FirstMessageDate: first, LastMessageDate: last,
				SubjectJobSignalCount: test.Signals.Job, SubjectApplicationSignalCount: test.Signals.Application,
				SubjectServiceSignalCount: test.Signals.Service,
			})
			if !reflect.DeepEqual(got, test.Want) {
				t.Errorf("suggestion = %+v, want %+v", got, test.Want)
			}
		})
	}
}

func TestUnknownSenderMalformedDateIsNeutral(t *testing.T) {
	t.Parallel()
	for _, dates := range [][2]string{
		{"2026-08-15", "2026-08-01"}, {"0000-01-01", "2026-08-15"},
		{"2026-02-30", "2026-08-15"}, {"2026-W31-9", "2026-W33-1"},
	} {
		got := SuggestUnknownSender(UnknownSenderSignals{
			SenderAddress: "digest@example.test", MessageCount: 4, DistinctMessageDays: 3,
			FirstMessageDate: dates[0], LastMessageDate: dates[1],
		})
		if got.ObservedSpanDays != 0 || got.Recommendation != "none" || len(got.SuggestionReasons) != 0 {
			t.Errorf("malformed/reversed date created a recommendation: %+v", got)
		}
	}
}

func TestUnknownSenderOversizedAddressIsNeutral(t *testing.T) {
	t.Parallel()
	got := SuggestUnknownSender(UnknownSenderSignals{
		SenderAddress:    strings.Repeat("x", 321) + "@jobs2web.com",
		FirstMessageDate: strings.Repeat("2", 10_000), LastMessageDate: "2026-08-15",
	})
	if got.Recommendation != "none" || len(got.SuggestionReasons) != 0 || got.ObservedSpanDays != 0 {
		t.Fatalf("untrusted oversized input created a recommendation: %+v", got)
	}
}
