//go:build windows

package runtimepaths

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectoryReservationNative(t *testing.T) {
	dir := testHome(t)
	if err := os.Mkdir(filepath.Join(dir, "staging"), 0700); err != nil {
		t.Fatal(err)
	}
	r := opened(t, dir)
	g, outcome, p := reserveDirectory(r, "staging/new", directoryReservationOperations{})
	if p != nil || outcome != DirectoryCreated || g == nil {
		t.Fatal(outcome, p)
	}
	if p = g.Check(); p != nil {
		t.Fatal(p)
	}
	if err := os.Rename(filepath.Join(dir, "staging", "new"), filepath.Join(dir, "staging", "renamed")); err == nil {
		t.Fatal("reserved directory renamed")
	}
	if err := os.Rename(filepath.Join(dir, "staging"), filepath.Join(dir, "moved")); err == nil {
		t.Fatal("reserved parent renamed")
	}
	// A namespace reservation deliberately does not freeze child writes.
	put(t, dir, "staging/new/file", "retained")
	if p = g.Check(); p != nil {
		t.Fatal(p)
	}
	if p = g.Close(); p != nil {
		t.Fatal(p)
	}
	if p = g.Close(); p != nil {
		t.Fatal("non-idempotent close", p)
	}
	code(t, g.Check(), "DIRECTORY_RESERVATION_CLOSED")
	if err := os.Rename(filepath.Join(dir, "staging", "new"), filepath.Join(dir, "staging", "renamed")); err != nil {
		t.Fatal("pin leaked", err)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "staging", "renamed", "file")); err != nil || string(raw) != "retained" {
		t.Fatal("close removed contents", err)
	}
	for _, path := range []string{"staging/renamed", "missing/new", "../escape", "staging/renamed/file/child"} {
		if g, o, p := reserveDirectory(r, path, directoryReservationOperations{}); g != nil || o != DirectoryNotCreated || p == nil {
			t.Fatal(path, o, p)
		}
	}
	if g, o, p := r.ReserveDirectory("staging/unowned"); g != nil || o != DirectoryNotCreated || p == nil {
		t.Fatal("unowned reservation admitted", o, p)
	}
}

func TestDirectoryReservationFaultFacts(t *testing.T) {
	for _, scenario := range []string{"collision", "committed-error", "unknown", "root-close"} {
		t.Run(scenario, func(t *testing.T) {
			dir := testHome(t)
			r := opened(t, dir)
			ops := directoryReservationOperations{}
			want := DirectoryCreated
			switch scenario {
			case "collision":
				want = DirectoryNotCreated
				ops.beforeCreate = func() {
					if err := os.Mkdir(filepath.Join(dir, "new"), 0700); err != nil {
						t.Fatal(err)
					}
					put(t, dir, "new/foreign", "preserve")
				}
			case "committed-error":
				ops.create = func(parent *os.Root, name string) (*os.File, DirectoryCreationOutcome, *Error) {
					f, o, p := createReservedDirectory(parent, name)
					if p != nil {
						t.Fatal(p)
					}
					return f, o, failure("SYNTHETIC_POST_CREATE")
				}
			case "unknown":
				want = DirectoryCreationUnknown
				ops.create = func(parent *os.Root, name string) (*os.File, DirectoryCreationOutcome, *Error) {
					f, _, p := createReservedDirectory(parent, name)
					if p != nil {
						t.Fatal(p)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
					return nil, DirectoryCreationUnknown, failure("DIRECTORY_CREATE_UNCONFIRMED")
				}
			}
			g, o, p := reserveDirectory(r, "new", ops)
			if o != want {
				t.Fatal(o, p)
			}
			if scenario == "root-close" {
				if p != nil || g == nil {
					t.Fatal(p)
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				code(t, g.Close(), "ROOT_CLOSED")
				code(t, g.Close(), "ROOT_CLOSED")
			} else if g != nil || p == nil {
				t.Fatal("failed reservation returned as ready", p)
			}
			if err := os.Rename(filepath.Join(dir, "new"), filepath.Join(dir, "after")); err != nil {
				t.Fatal("error leaked pin", err)
			}
			if scenario == "collision" {
				if data, err := os.ReadFile(filepath.Join(dir, "after", "foreign")); err != nil || string(data) != "preserve" {
					t.Fatal("foreign directory changed")
				}
			}
		})
	}
}

func TestDirectoryReservationOwnedProcess(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDirectoryReservationOwnedHelper$")
	cmd.Env = append(os.Environ(), "CH_DIRECTORY_RESERVATION_HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned helper %v\n%s", err, out)
	}
}

func TestDirectoryReservationOwnedHelper(t *testing.T) {
	dir := os.Getenv("CH_DIRECTORY_RESERVATION_HOME")
	if dir == "" {
		return
	}
	r := opened(t, dir)
	if p := r.ClaimForProcess(); p != nil {
		t.Fatal(p)
	}
	g, o, p := r.ReserveDirectory("new")
	if p != nil || g == nil || o != DirectoryCreated {
		t.Fatal(o, p)
	}
	if p = g.Close(); p != nil {
		t.Fatal(p)
	}
	if g, o, p := r.ReserveDirectory("new"); p == nil || g != nil || o != DirectoryNotCreated {
		t.Fatal("adopted existing namespace", o, p)
	}
}
