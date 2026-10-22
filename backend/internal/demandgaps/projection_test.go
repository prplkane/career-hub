package demandgaps

import "testing"

func TestRequestCalendarBoundaries(t *testing.T) {
	for _, r := range []Request{{From: "2024-02-29", To: "2024-02-29", AsOf: "2026-09"}, {}, {From: "0001-01-01", To: "9999-12-31"}} {
		if r.Validate() != nil {
			t.Fatal("valid calendar rejected", r)
		}
	}
	for _, r := range []Request{{From: "2025-02-29"}, {From: "2026-01-02", To: "2026-01-01"}, {From: "0000-01-01"}, {From: "2026-1-01"}, {To: "2026-01-01T00:00:00"}, {AsOf: "2026-13"}, {AsOf: "0000-01"}, {AsOf: "2026-09-01"}} {
		if r.Validate() == nil {
			t.Fatal("invalid calendar accepted", r)
		}
	}
}
