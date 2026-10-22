package runtimepaths

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadSingleLinkWindowBoundsAndEOF(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "events.jsonl")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, problem := Open(base)
	if problem != nil {
		t.Fatal(problem)
	}
	defer root.Close()
	for _, tc := range []struct {
		offset, limit int64
		want          string
	}{{0, 3, "alp"}, {3, 6, "ha\nbet"}, {9, 8, "a\n"}, {11, 8, ""}} {
		got, size, present, problem := root.ReadSingleLinkWindow("events.jsonl", tc.offset, tc.limit)
		if problem != nil || !present || size != 11 || string(got) != tc.want {
			t.Fatalf("window %d/%d: %q size=%d present=%v problem=%v", tc.offset, tc.limit, got, size, present, problem)
		}
	}
	if got, size, present, problem := root.ReadSingleLinkLegacyLogWindow("events.jsonl", 6, 8); problem != nil || !present || size != 11 || string(got) != "beta\n" {
		t.Fatalf("legacy window changed guarded read semantics: %q %d %v %v", got, size, present, problem)
	}
	if got, size, present, problem := root.readSingleLinkWindowWithMax("events.jsonl", 0, 8, 10, windowOperations{}); got != nil || size != 0 || present || problem == nil || problem.Code != "FILE_SIZE_LIMIT" {
		t.Fatalf("custom total-size ceiling was ignored: %q %d %v %v", got, size, present, problem)
	}
	if got, size, present, problem := root.ReadSingleLinkWindow("missing.jsonl", 0, 8); got != nil || size != 0 || present || problem != nil {
		t.Fatalf("missing file was not absent: %q %d %v %v", got, size, present, problem)
	}
	for _, tc := range []struct {
		offset, limit int64
		code          string
	}{{-1, 8, "READ_LIMIT_INVALID"}, {0, 0, "READ_LIMIT_INVALID"}, {0, MaxWindowReadBytes + 1, "READ_LIMIT_INVALID"}, {12, 8, "READ_OFFSET_INVALID"}} {
		got, size, present, problem := root.ReadSingleLinkWindow("events.jsonl", tc.offset, tc.limit)
		if got != nil || size != 0 || present || problem == nil || problem.Code != tc.code {
			t.Fatalf("invalid window %d/%d exposed data: %q %d %v %v", tc.offset, tc.limit, got, size, present, problem)
		}
	}
	if err := os.Truncate(path, MaxReadBytes+1); err != nil {
		t.Fatal(err)
	}
	if got, size, present, problem := root.ReadSingleLinkWindow("events.jsonl", 0, 8); got != nil || size != 0 || present || problem == nil || problem.Code != "FILE_SIZE_LIMIT" {
		t.Fatalf("oversized file was windowed: %q %d %v %v", got, size, present, problem)
	}
}

func TestReadSingleLinkWindowRefusesHardlinkAndChangedFile(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "events.jsonl")
	initial := []byte("start\nfinish\n")
	if err := os.WriteFile(path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	root, problem := Open(base)
	if problem != nil {
		t.Fatal(problem)
	}
	defer root.Close()
	changed := false
	got, size, present, problem := root.readSingleLinkWindow("events.jsonl", 0, 8, windowOperations{
		readAt: func(file *os.File, buf []byte, offset int64) (int, error) {
			n, err := file.ReadAt(buf, offset)
			if err == nil {
				if e := os.Chtimes(path, time.Unix(1, 0), time.Unix(1, 0)); e != nil {
					t.Fatal(e)
				}
				changed = true
			}
			return n, err
		},
	})
	if !changed || got != nil || size != 0 || present || problem == nil || problem.Code != "PATH_CHANGED" {
		t.Fatalf("changed file leaked a window: %q %d %v %v", got, size, present, problem)
	}
	alias := filepath.Join(base, "unrelated.txt")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	got, size, present, problem = root.ReadSingleLinkWindow("events.jsonl", 0, 8)
	if got != nil || size != 0 || present || problem == nil || problem.Code != "PATH_HARDLINK" {
		t.Fatalf("hardlinked file leaked a window: %q %d %v %v", got, size, present, problem)
	}
	if after, err := os.ReadFile(alias); err != nil || !bytes.Equal(after, initial) {
		t.Fatalf("read changed hardlink content: %v", err)
	}
}
