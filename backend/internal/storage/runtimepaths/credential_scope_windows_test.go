//go:build windows

package runtimepaths

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCredentialScopeNativeQueryFailuresAndBoundedGrowth(t *testing.T) {
	file, err := os.Open(testHome(t))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, mode := range []string{"normal", "growth", "identity-error", "changed", "zero-id", "path-error", "zero-length", "unbounded", "twice-grown", "unterminated"} {
		t.Run(mode, func(t *testing.T) {
			identityCalls, pathCalls := 0, 0
			queries := credentialScopeQueries{
				identity: func(windows.Handle) (credentialFileID, error) {
					identityCalls++
					info := credentialFileID{VolumeSerial: 17, FileID: scopeFixtureID()}
					if mode == "identity-error" {
						return info, errors.New("private cause")
					}
					if mode == "zero-id" {
						info.FileID = [16]byte{}
					}
					if mode == "changed" && identityCalls > 1 {
						info.VolumeSerial++
					}
					return info, nil
				},
				path: func(_ windows.Handle, output *uint16, count, flags uint32) (uint32, error) {
					pathCalls++
					if flags != 1 {
						t.Fatal("opened/DOS path flags used")
					}
					if mode == "path-error" {
						return 0, errors.New("private path")
					}
					if mode == "zero-length" {
						return 0, nil
					}
					if mode == "unbounded" {
						return maxCredentialPathUnits + 1, nil
					}
					if mode == "twice-grown" {
						return count + 1, nil
					}
					path := scopeFixturePath
					if mode == "growth" {
						path += strings.Repeat("x", 600)
					}
					units := utf16.Encode([]rune(path))
					if uint32(len(units)+1) > count {
						return uint32(len(units) + 1), nil
					}
					buffer := unsafe.Slice(output, int(count))
					copy(buffer, units)
					if mode != "unterminated" {
						buffer[len(units)] = 0
					}
					return uint32(len(units)), nil
				},
			}
			scope, e := queryCredentialScope(file, queries)
			good := mode == "normal" || mode == "growth"
			if good != (e == nil && scope.Valid()) || !good && (e == nil || e.Code != "CREDENTIAL_SCOPE_UNAVAILABLE") {
				t.Fatal("query classification changed")
			}
			if pathCalls > 2 || good && identityCalls != 2 || mode == "growth" && pathCalls != 2 {
				t.Fatal("unbounded query/recheck missing")
			}
		})
	}
}

func TestCredentialScopeProcessHelper(t *testing.T) {
	rootPath := os.Getenv("CAREERHUB_SCOPE_TEST_ROOT")
	if rootPath == "" {
		return
	}
	root, e := Open(rootPath)
	if e != nil {
		t.Fatal(e)
	}
	if scope, e := root.CredentialScope(); e == nil || e.Code != "OWNERSHIP_REQUIRED" || scope.Valid() {
		t.Fatal("unowned scope admitted")
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	scope, e := root.CredentialScope()
	if e != nil {
		t.Fatal(e)
	}
	again, e := root.CredentialScope()
	if e != nil || !scope.Equal(again) {
		t.Fatal("held scope changed")
	}
	if e := root.Close(); e != nil {
		t.Fatal(e)
	}
	if closed, e := root.CredentialScope(); e == nil || e.Code != "ROOT_CLOSED" || closed.Valid() {
		t.Fatal("closed scope admitted")
	}
	fmt.Println("scope=" + scope.Fingerprint())
}

func TestCredentialScopeNativeRestartsDistinctRootsMovesAndReplacement(t *testing.T) {
	base := testHome(t)
	a, b, moved := filepath.Join(base, "A"), filepath.Join(base, "B"), filepath.Join(base, "Moved")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCredentialScopeProcessHelper$")
		cmd.Env = append(os.Environ(), "CAREERHUB_SCOPE_TEST_ROOT="+path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("native scope helper failed: %s", out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "scope=") {
				digest := strings.TrimSpace(strings.TrimPrefix(line, "scope="))
				if len(digest) == 64 {
					return digest
				}
			}
		}
		t.Fatal("native scope missing")
		return ""
	}
	first := read(a)
	if first != read(a) || first != read(strings.ToUpper(a)) || first == read(b) {
		t.Fatal("restart/copy identity incorrect")
	}
	if err := os.Rename(a, moved); err != nil {
		t.Fatal(err)
	}
	if first == read(moved) {
		t.Fatal("moved root retained scope")
	}
	if err := os.Mkdir(a, 0700); err != nil {
		t.Fatal(err)
	}
	if first == read(a) {
		t.Fatal("replacement root retained scope")
	}
}
