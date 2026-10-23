package discovery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestDeleteRequestBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, input := range []DeleteRequest{
		{Operation: "other"},
		{Operation: "delete", DiscoveryID: "."},
		{Operation: "delete", DiscoveryID: "a/b"},
		{Operation: "delete", DiscoveryID: "a", ExpectedRevision: true},
		{Operation: "delete", DiscoveryID: "a", ExpectedRevision: json.Number("1.0")},
		{Operation: "delete", DiscoveryID: "a", ExpectedRevision: 0},
		{Operation: "flush", DiscoveryID: "a"},
		{Operation: "flush", ExpectedRevision: 1},
	} {
		if result, e := DeleteInTransaction(ctx, nil, nil, input); e == nil || result != nil || e.Code == "DATABASE_CONNECTION_INVALID" {
			t.Fatal("invalid input reached transaction", input, e)
		}
	}
	for _, input := range []DeleteRequest{{Operation: "delete", DiscoveryID: "a"}, {Operation: "delete", DiscoveryID: ".."}, {Operation: "delete", DiscoveryID: "a", ExpectedRevision: 1}, {Operation: "flush"}} {
		if _, e := DeleteInTransaction(ctx, nil, nil, input); e == nil || e.Code != "DATABASE_CONNECTION_INVALID" {
			t.Fatal("nil transaction", e)
		}
	}
}

func TestDeletionSourcePathAndByteLimits(t *testing.T) {
	r := &reader{ctx: context.Background(), remaining: MaxReadBytes}
	for _, path := range []string{"jobs/discovery/captures/../else.txt", "jobs/discovery/captures/other/source.txt", "jobs/discovery/captures/a/source.txt:stream", "jobs/discovery/captures/a/../source.txt", "jobs\\discovery\\captures\\a\\source.txt"} {
		files := &captureFault{present: true}
		if _, e := deletionSources(r, files, []deletionTarget{{"a", path}}); e == nil || e.Code != "SOURCE_PATH_REJECTED" || files.calls != 0 {
			t.Fatal("unsafe deletion read", e)
		}
	}
	for _, fault := range []*captureFault{
		{err: &runtimepaths.Error{Code: "PATH_ALIAS"}},
		{raw: make([]byte, (4<<20)+1), present: true},
	} {
		if _, e := deletionSources(r, fault, []deletionTarget{{"a", "jobs/discovery/captures/a/source.txt"}}); e == nil {
			t.Fatal("unbounded/unsafe source accepted")
		}
	}
	ids := []deletionTarget{{"a", "jobs/discovery/captures/a/source.txt"}, {"b", "jobs/discovery/captures/b/source.txt"}, {"c", "jobs/discovery/captures/c/source.txt"}}
	if _, e := deletionSources(r, &captureFault{raw: make([]byte, 4<<20), present: true}, ids); e == nil || e.Code != "RESULT_LIMIT" {
		t.Fatal("aggregate source limit", e)
	}
	if _, e := deletionSources(r, &captureFault{}, []deletionTarget{{"a", "jobs/discovery/captures/a/source.txt"}, {"A", "jobs/discovery/captures/A/source.txt"}}); e == nil || e.Code != "SOURCE_REFERENCE_CONFLICT" {
		t.Fatal("case-alias target conflict", e)
	}
	if _, e := capturePath(strings.Repeat("a", 301)); e == nil {
		t.Fatal("unbounded source identity")
	}
	if _, e := capturePath(".."); e == nil {
		t.Fatal("logical parent identifier became a file path")
	}
}
