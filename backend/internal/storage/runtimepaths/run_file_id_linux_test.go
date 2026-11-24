package runtimepaths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestObservedRunsReplacementWithIdenticalMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "run", "review.yaml")
	stamp := time.Unix(1700000000, 0)
	writeReport := func() {
		t.Helper()
		if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	writeReport()
	root, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	runs, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	if m, e := runs.ChildMetadata("run", "review.yaml"); e != nil || !m.Present {
		t.Fatal(m, e)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeReport()
	if file, e := runs.ReadChildFile("run", "review.yaml", 16); e == nil || e.Code != "PATH_CHANGED" || file.Present || file.Content != nil {
		t.Fatalf("replacement read: %+v %v", file, e)
	}
	if e := runs.Recheck(); e == nil || e.Code != "PATH_CHANGED" {
		t.Fatalf("replacement recheck: %v", e)
	}
}

// Validate both ways to retain an inode on a filesystem without export handles.
// Dropping all permissions also exercises the descriptor fallback for ordinary users.
func TestRunFilePinReleasedByRoot(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "empty-report")
			if err := os.WriteFile(path, nil, mode); err != nil {
				t.Fatal(err)
			}
			dir, err := os.OpenRoot(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			file, err := dir.OpenFile(filepath.Base(path), unix.O_PATH|unix.O_NOFOLLOW, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			root := &Root{}
			defer root.Close()
			adopted, err := root.runFiles.retain(info, file)
			if err != nil {
				t.Fatal(err)
			}
			// Probe OS mapping support independently of the production pin.
			probe, openErr := os.Open(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
			if openErr == nil {
				page, mapErr := unix.Mmap(int(probe.Fd()), 0, 1, unix.PROT_NONE, unix.MAP_PRIVATE)
				if err := probe.Close(); err != nil {
					t.Fatal(err)
				}
				if mapErr == nil {
					if err := unix.Munmap(page); err != nil {
						t.Fatal(err)
					}
					if adopted {
						t.Fatal("kept a descriptor despite available file mapping")
					}
				}
			}
			pin := root.runFiles.pins[inodeOf(info)]
			if (pin.file != nil) != adopted || (pin.mapping == nil) != adopted {
				t.Fatal("expected exactly one owned pin")
			}
			second, err := dir.OpenFile(filepath.Base(path), unix.O_PATH|unix.O_NOFOLLOW, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			duplicate, err := root.runFiles.retain(info, second)
			if err != nil || duplicate || root.runFiles.object(second, info) != (runFileID{}) {
				t.Fatal("duplicate pin or changed identity", err)
			}
			second.Close()
			if !adopted {
				file.Close()
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			replacement, err := os.Stat(path)
			if err != nil || os.SameFile(info, replacement) {
				t.Fatal("retained inode reused for replacement", err)
			}
			if err := root.Close(); err != nil {
				t.Fatal(err)
			}
			if adopted {
				if _, err := file.Stat(); !errors.Is(err, fs.ErrClosed) {
					t.Fatalf("descriptor after root close: %v", err)
				}
			} else if err := unix.Munmap(pin.mapping); !errors.Is(err, unix.EINVAL) {
				t.Fatalf("mapping after root close: %v", err)
			}
		})
	}
}
