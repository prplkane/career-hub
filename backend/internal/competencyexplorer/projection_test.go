package competencyexplorer

import (
	"context"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/calendarinput"
)

func TestUnionAdjacentOverlapFutureAndCertificationScope(t *testing.T) {
	end := func(s string) *string { return &s }
	values := []*calendarinput.Range{{Start: "2024-01", End: end("2024-06")}, {Start: "2024-04", End: end("2024-08")}, {Start: "2024-09", End: end("2024-10")}, {Start: "2025-01", Ongoing: true}, {Start: "2090-01", Ongoing: true}, nil}
	got, e := union(values, "2026-09")
	want := []span{{2024 * 12, 2024*12 + 9, false}, {2025 * 12, 2026*12 + 8, true}}
	if e != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, e)
	}
	for _, month := range []string{"", "2026-00", "2026-13", "x026-01"} {
		if _, e := union(nil, month); e == nil {
			t.Fatal("bad month accepted")
		}
	}
	if _, e := union([]*calendarinput.Range{{Start: "2024-02", End: end("2024-01")}}, "2026-09"); e == nil {
		t.Fatal("inverted range accepted")
	}
	if _, e := Build(context.Background(), nil, nil, "x", "2026-09", false); e == nil {
		t.Fatal("missing registry accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Build(ctx, nil, nil, "x", "2026-09", false); e == nil {
		t.Fatal("cancellation ignored")
	}
}
