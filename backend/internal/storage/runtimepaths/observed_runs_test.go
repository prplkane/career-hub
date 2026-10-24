package runtimepaths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestObservedRunsMetadataWitness(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "run")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(child, "extracted.yaml")
	if err := os.WriteFile(leaf, []byte("not yaml; metadata only"), 0600); err != nil {
		t.Fatal(err)
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	metadata, e := observed.ChildMetadata("run", "extracted.yaml")
	if e != nil || !metadata.Present || metadata.Size != int64(len("not yaml; metadata only")) {
		t.Fatal(metadata, e)
	}
	absent, e := observed.ChildMetadata("run", "review.yaml")
	if e != nil || absent.Present {
		t.Fatal(absent, e)
	}
	if e := observed.Recheck(); e != nil {
		t.Fatal(e)
	}
	if err := os.WriteFile(leaf, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, e := observed.ReadChildFile("run", "extracted.yaml", 64); e == nil || e.Code != "PATH_CHANGED" {
		t.Fatal("metadata/read drift accepted", e)
	}
	if e := observed.Recheck(); e == nil {
		t.Fatal("drift accepted")
	}
	fresh, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if _, e := fresh.ChildMetadata("run", "review.yaml"); e != nil {
		t.Fatal(e)
	}
	if err := os.WriteFile(filepath.Join(child, "review.yaml"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if e := fresh.Recheck(); e == nil {
		t.Fatal("negative witness changed")
	}
	if _, e := fresh.ChildMetadata("run", "private.txt"); e == nil {
		t.Fatal("unknown leaf accepted")
	}
	if _, e := fresh.ChildMetadata("../escape", "extracted.yaml"); e == nil {
		t.Fatal("unknown run accepted")
	}
	fresh.closeChild = func(child *os.Root) error { _ = child.Close(); return errors.New("synthetic cleanup failure") }
	if result, e := fresh.ChildMetadata("run", "extracted.yaml"); e == nil || e.Code != "ROOT_CLOSE" || result != (ObservedMetadata{}) {
		t.Fatal("cleanup failure retained metadata", result, e)
	}
}

func TestObservedRunsConcurrentReadsAndExclusiveRecheck(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"run_a", "run_b"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "extracted.yaml"), []byte("fictional"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	observed.closeChild = func(child *os.Root) error {
		entered <- struct{}{}
		<-release
		return child.Close()
	}
	var workers sync.WaitGroup
	for _, name := range []string{"run_a", "run_b"} {
		workers.Add(1)
		go func(name string) {
			defer workers.Done()
			if file, e := observed.ReadChildFile(name, "extracted.yaml", 16); e != nil || !file.Present {
				t.Errorf("%s: %+v %v", name, file, e)
			}
		}(name)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			workers.Wait()
			t.Fatal("reads serialized")
		}
	}
	rechecked := make(chan *Error, 1)
	go func() { rechecked <- observed.Recheck() }()
	select {
	case <-rechecked:
		t.Fatal("recheck ran during reads")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	workers.Wait()
	if e := <-rechecked; e != nil {
		t.Fatal(e)
	}
}

func TestObservedRunsConcurrentSameLeafWitnessDrift(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(dir, "run", "extracted.yaml")
	if err := os.WriteFile(leaf, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if _, e := observed.ReadChildFile("run", "extracted.yaml", 16); e != nil {
		t.Fatal(e)
	}
	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if file, e := observed.ReadChildFile("run", "extracted.yaml", 16); e == nil || e.Code != "PATH_CHANGED" || !reflect.DeepEqual(file, ObservedFile{}) {
				t.Errorf("drift: %+v %v", file, e)
			}
		}()
	}
	workers.Wait()
}

func TestObservedRunsPositiveNegativeAndDrift(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"run_b", "run_a"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "run_a", "extracted.yaml"), []byte("fictional"), 0600); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(filepath.Join(dir, "run_a", "extracted.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(observed.Names(), []string{"run_a", "run_b"}) {
		t.Fatal(observed.Names())
	}
	if file, e := observed.ReadChildFile("run_a", "extracted.yaml", 16); e != nil || !file.Present || string(file.Content) != "fictional" || !file.Modified.Equal(stat.ModTime()) {
		t.Fatalf("present %+v %v", file, e)
	}
	if file, e := observed.ReadChildFile("run_a", "review.yaml", 16); e != nil || file.Present || file.Content != nil || !file.Modified.IsZero() {
		t.Fatalf("missing %+v %v", file, e)
	}
	if e := observed.Recheck(); e != nil {
		t.Fatal(e)
	}
	if err := os.WriteFile(filepath.Join(dir, "run_a", "review.yaml"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if e := observed.Recheck(); e == nil || e.Code != "PATH_CHANGED" {
		t.Fatalf("negative witness drift: %v", e)
	}
	if _, e := observed.ReadChildFile("run_a", "other.yaml", 10); e == nil || e.Code != "PATH_INVALID" {
		t.Fatalf("nonfixed leaf: %v", e)
	}
}

func TestObservedRunNameUsesExactEntryAndPortableCollisionKey(t *testing.T) {
	for _, name := range []string{"bad.", "CON", "bad/name"} {
		if normalized, e := observedRunName(name); normalized != "" || e == nil {
			t.Fatalf("accepted nonportable run name %q: %q %v", name, normalized, e)
		}
	}
	name := "re\u0301sume\u0301"
	if normalized, e := observedRunName(name); e != nil || normalized != "r\u00e9sum\u00e9" {
		t.Fatalf("normalized collision key: %q %v", normalized, e)
	}
}

func TestObservedRunsRejectsReplacedChildAndNewMember(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "run")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if err := os.Rename(child, filepath.Join(dir, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if file, e := observed.ReadChildFile("run", "review.yaml", 16); e == nil || e.Code != "PATH_CHANGED" || !reflect.DeepEqual(file, ObservedFile{}) {
		t.Fatalf("replacement: %+v %v", file, e)
	}
	if e := observed.Recheck(); e == nil || e.Code != "PATH_CHANGED" {
		t.Fatalf("membership: %v", e)
	}
}

func TestObservedRunsManyChildrenWithoutPerReadParentScan(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 1001; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("run_%04d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range observed.Names() {
		if file, e := observed.ReadChildFile(name, "approval_result.yaml", 16); e != nil || file.Present || file.Content != nil || !file.Modified.IsZero() {
			t.Fatalf("%s: %+v %v", name, file, e)
		}
	}
	if observed.parentScans != 1 {
		t.Fatalf("per-child parent rescan: %d", observed.parentScans)
	}
	if e := observed.Recheck(); e != nil {
		t.Fatal(e)
	}
	if observed.parentScans != 2 {
		t.Fatalf("final scan count: %d", observed.parentScans)
	}
}

func TestObservedRunsLimitAndClose(t *testing.T) {
	dir := t.TempDir()
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if e := root.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := observed.ReadChildFile("run", "review.yaml", 16); e == nil || e.Code != "ROOT_CLOSED" {
		t.Fatalf("closed read: %v", e)
	}
	if e := observed.Recheck(); e == nil || e.Code != "ROOT_CLOSED" {
		t.Fatalf("closed recheck: %v", e)
	}
	if got, e := root.ObserveRunDirectories(); got != nil || e == nil || e.Code != "ROOT_CLOSED" {
		t.Fatalf("closed scan: %v", e)
	}
}

func TestObservedRunsChildCloseFailureDiscardsRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run", "review.yaml"), []byte("fictional"), 0600); err != nil {
		t.Fatal(err)
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	observed.closeChild = func(child *os.Root) error { _ = child.Close(); return errors.New("synthetic close failure") }
	if file, e := observed.ReadChildFile("run", "review.yaml", 32); !reflect.DeepEqual(file, ObservedFile{}) || e == nil || e.Code != "ROOT_CLOSE" {
		t.Fatalf("late close exposed result: %+v %v", file, e)
	}
	if e := observed.Recheck(); e == nil || e.Code != "ROOT_CLOSE" {
		t.Fatalf("recheck close: %v", e)
	}
}

func TestObservedRunsPositiveWitnessAndLeafLimit(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "run")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(child, "extracted.yaml")
	if err := os.WriteFile(leaf, []byte("fictional"), 0600); err != nil {
		t.Fatal(err)
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if file, e := observed.ReadChildFile("run", "extracted.yaml", 3); !reflect.DeepEqual(file, ObservedFile{}) || e == nil || e.Code != "FILE_SIZE_LIMIT" {
		t.Fatalf("over-limit result: %+v %v", file, e)
	}
	if file, e := observed.ReadChildFile("run", "extracted.yaml", 16); e != nil || !file.Present || file.Modified.IsZero() {
		t.Fatalf("read: %+v %v", file, e)
	}
	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, []byte("fictional"), 0600); err != nil {
		t.Fatal(err)
	}
	if e := observed.Recheck(); e == nil || e.Code != "PATH_CHANGED" {
		t.Fatalf("positive witness drift: %v", e)
	}
}

func TestObservedRunsChildArtifactEntryLimit(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "run")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "extracted.yaml"), []byte("fictional"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxRunArtifactEntries-1; i++ {
		if err := os.WriteFile(filepath.Join(child, fmt.Sprintf("extra_%03d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if file, e := observed.ReadChildFile("run", "extracted.yaml", 16); e != nil || !file.Present {
		t.Fatalf("at limit: %+v %v", file, e)
	}
	if err := os.WriteFile(filepath.Join(child, "extra_over_limit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if file, e := observed.ReadChildFile("run", "extracted.yaml", 16); !reflect.DeepEqual(file, ObservedFile{}) || e == nil || e.Code != "PATH_DIRECTORY_LIMIT" {
		t.Fatalf("over limit: %+v %v", file, e)
	}
}

func TestObservedRunsRejectsCaseAliasLeaf(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "run")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "Extracted.yaml"), []byte("fictional"), 0600); err != nil {
		t.Fatal(err)
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if file, e := observed.ReadChildFile("run", "extracted.yaml", 16); !reflect.DeepEqual(file, ObservedFile{}) || e == nil || e.Code != "PATH_CASE_COLLISION" {
		t.Fatalf("case alias: %+v %v", file, e)
	}
}

func TestObservedRunsSkipOnlyRegularPlaceholderFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "run_a"), 0700); err != nil {
		t.Fatal(err)
	}
	// Python-era Homes carry a .gitkeep from the repository template; Windows
	// adds desktop.ini and Thumbs.db. None of them may hide the real runs.
	for _, name := range []string{".gitkeep", "desktop.ini", "Thumbs.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatalf("placeholder files must not block the scan: %v", e)
	}
	if !reflect.DeepEqual(observed.Names(), []string{"run_a"}) {
		t.Fatal(observed.Names())
	}
	if e := observed.Recheck(); e != nil {
		t.Fatalf("recheck with placeholders: %v", e)
	}
	root.Close()

	// Any other stray file still fails closed.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	root, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := root.ObserveRunDirectories(); e == nil || e.Code != "PATH_NOT_DIRECTORY" {
		t.Fatalf("unknown file must still fail: %v", e)
	}
	root.Close()
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}

	// A directory that borrows a placeholder name is not skipped silently.
	if err := os.Remove(filepath.Join(dir, ".gitkeep")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".gitkeep"), 0700); err != nil {
		t.Fatal(err)
	}
	root, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if observed, e := root.ObserveRunDirectories(); e == nil && reflect.DeepEqual(observed.Names(), []string{"run_a"}) {
		t.Fatal("a .gitkeep directory must not be treated as an ignorable placeholder file")
	}
}
