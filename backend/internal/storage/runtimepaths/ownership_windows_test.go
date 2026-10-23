//go:build windows

package runtimepaths

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestOwnershipNativePinnedHandleQueries(t *testing.T) {
	root := opened(t, testHome(t))
	for i, original := range root.handles {
		pin, e := pinOwnershipRoot(original)
		if e != nil {
			t.Fatalf("native pin failed at ancestor %d: %s", i, e.Code)
		}
		if _, err := pin.Stat(); err != nil {
			_ = pin.Close()
			t.Fatalf("pinned stat failed at ancestor %d", i)
		}
		if i == len(root.handles)-1 {
			if e := checkLocalVolume(pin); e != nil {
				_ = pin.Close()
				t.Fatalf("pinned volume query: %s", e.Code)
			}
		}
		if err := pin.Close(); err != nil {
			t.Fatal("native pin close failed")
		}
	}
}

func TestOwnershipNativeExclusionIdentityAndSecurity(t *testing.T) {
	rootPath := testHome(t)
	root := opened(t, rootPath)
	c, e := acquireProcessClaim(root)
	if e != nil {
		t.Fatal(e)
	}
	defer c.close()
	var flags uint32
	getFlags := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	result, _, _ := getFlags.Call(uintptr(c.token), uintptr(unsafe.Pointer(&flags)))
	if result == 0 || flags&windows.HANDLE_FLAG_INHERIT != 0 {
		t.Fatal("ownership token is inheritable or its flags are unavailable")
	}
	sd, err := windows.GetSecurityInfo(c.token, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal("ownership DACL unavailable")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal("test token unavailable")
	}
	sid := user.User.Sid.String()
	expectedText := "D:P(A;;0x00120000;;;" + sid + ")"
	if sid != "S-1-5-18" {
		expectedText += "(A;;0x00120000;;;SY)"
	}
	expected, err := windows.SecurityDescriptorFromString(expectedText)
	if err != nil || sd.String() != expected.String() {
		t.Fatal("ownership DACL is not exactly current principal plus SYSTEM")
	}
	// Configured case spelling and a separately opened Root must not split a
	// claim for one directory. No named-object key is returned to the caller.
	alias := opened(t, strings.ToUpper(rootPath))
	if !processRootOwned(c, alias) || processRootOwned(c, opened(t, testHome(t))) {
		t.Fatal("ownership observation did not use exact native directory identity")
	}
	other, e := acquireProcessClaim(alias)
	if other != nil {
		_ = other.close()
		t.Fatal("second root claimed the same native directory")
	}
	code(t, e, "OWNERSHIP_IN_USE")
	independent, e := acquireProcessClaim(opened(t, testHome(t)))
	if e != nil {
		t.Fatal("different home unnecessarily contended", e)
	}
	if e := independent.close(); e != nil {
		t.Fatal(e)
	}
	if e := c.close(); e != nil {
		t.Fatal(e)
	}
	reacquired, e := acquireProcessClaim(alias)
	if e != nil {
		t.Fatal("failed contender retained a token handle", e)
	}
	if e := reacquired.close(); e != nil {
		t.Fatal(e)
	}
}

func TestOwnershipNativeAtomicContenders(t *testing.T) {
	root := opened(t, testHome(t))
	type result struct {
		claim *processClaim
		err   *Error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			c, e := acquireProcessClaim(root)
			results <- result{c, e}
		}()
	}
	close(start)
	winners := 0
	for range 2 {
		got := <-results
		if got.claim != nil {
			winners++
			// Keep the winning handle until both native attempts have returned.
			defer got.claim.close()
			if got.err != nil {
				t.Fatal(got.err)
			}
		} else {
			code(t, got.err, "OWNERSHIP_IN_USE")
		}
	}
	if winners != 1 {
		t.Fatal("native simultaneous acquisition did not have exactly one winner")
	}
}

func TestOwnershipNativeForeignObjectAndInvalidIdentity(t *testing.T) {
	root := opened(t, testHome(t))
	code(t, root.RequireProcessOwnership(), "OWNERSHIP_REQUIRED")
	file, err := root.base.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	name, e := ownershipName(file)
	if e != nil || !strings.HasPrefix(name, `Global\CareerHub.DataHomeOwner.v1.`) {
		t.Fatal("invalid ownership namespace")
	}
	nativeName, _ := windows.UTF16PtrFromString(name)
	event, err := windows.CreateEventEx(nil, nativeName, 0, windows.READ_CONTROL)
	if err != nil {
		t.Fatal("synthetic foreign-object fixture unavailable")
	}
	defer windows.CloseHandle(event)
	token, e := acquireOwnershipToken(file)
	if token != 0 {
		_ = windows.CloseHandle(token)
		t.Fatal("foreign object was accepted as ownership")
	}
	code(t, e, "OWNERSHIP_UNAVAILABLE")
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	name, e = ownershipName(file)
	code(t, e, "OWNERSHIP_UNAVAILABLE")
	if name != "" {
		t.Fatal("invalid handle exposed a name")
	}
	var absent *Root
	code(t, absent.RequireProcessOwnership(), "ROOT_CLOSED")
	code(t, (&Root{}).RequireProcessOwnership(), "ROOT_CLOSED")
	code(t, absent.ClaimForProcess(), "ROOT_CLOSED")
	code(t, (&Root{}).ClaimForProcess(), "ROOT_CLOSED")
}

// This process must exit rather than return to an embedded caller: a successful
// published claim deliberately has no unlock API, even after caller Close/GC.
func TestOwnershipHelperProcess(t *testing.T) {
	path := os.Getenv("CAREERHUB_TEST_OWNERSHIP_ROOT")
	if path == "" {
		return
	}
	root, e := Open(path)
	if e == nil {
		e = root.ClaimForProcess()
	}
	if e == nil {
		e = root.RequireProcessOwnership()
	}
	if root != nil {
		_ = root.Close()
	}
	if e != nil {
		fmt.Fprintln(os.Stdout, "ERROR:"+e.Code)
		os.Exit(3)
	}
	root = nil
	runtime.GC()
	reopened, e := Open(path)
	if e == nil {
		e = reopened.RequireProcessOwnership()
	}
	if reopened != nil {
		_ = reopened.Close()
	}
	if e != nil {
		fmt.Fprintln(os.Stdout, "ERROR:"+e.Code)
		os.Exit(3)
	}
	fmt.Fprintln(os.Stdout, "OWNED")
	var control [1]byte
	n, _ := os.Stdin.Read(control[:])
	if n == 1 && control[0] == '!' {
		os.Exit(23)
	}
	os.Exit(0)
}

type ownershipChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	waited bool
}

func startOwnershipChild(t *testing.T, root string) *ownershipChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnershipHelperProcess$")
	cmd.Env = append(os.Environ(), "CAREERHUB_TEST_OWNERSHIP_ROOT="+root)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 500 * time.Millisecond
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	child := &ownershipChild{cmd: cmd, stdin: input, stdout: bufio.NewReader(io.LimitReader(output, 256))}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		t.Fatal("synthetic ownership child could not start")
	}
	t.Cleanup(func() {
		_ = child.stdin.Close()
		if !child.waited {
			_ = child.cmd.Process.Kill()
			_ = child.cmd.Wait()
		}
	})
	return child
}

func (c *ownershipChild) expect(t *testing.T, want string) {
	t.Helper()
	line, err := c.stdout.ReadString('\n')
	if err != nil || line != want+"\n" {
		t.Fatalf("ownership child response: %q; read error: %v", line, err)
	}
}

func (c *ownershipChild) wait(t *testing.T, want int) {
	t.Helper()
	c.waited = true
	err := c.cmd.Wait()
	if want == 0 {
		if err != nil {
			t.Fatal("synthetic child did not exit normally")
		}
		return
	}
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != want {
		t.Fatal("synthetic child exit did not match expected result")
	}
}

func TestOwnershipNativeProcessLifetimeAndCrashRecovery(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprintf("crash=%v", crash), func(t *testing.T) {
			dir := testHome(t)
			put(t, dir, "opaque", "untouched fixture")
			owner := startOwnershipChild(t, dir)
			owner.expect(t, "OWNED")
			contender := startOwnershipChild(t, strings.ToUpper(dir))
			contender.expect(t, "ERROR:OWNERSHIP_IN_USE")
			contender.wait(t, 3)
			moved := dir + "-moved"
			if err := os.Rename(dir, moved); err == nil {
				_ = os.Rename(moved, dir)
				t.Fatal("root was not pinned after caller Close and GC")
			}
			wantExit := 0
			if crash {
				_, _ = owner.stdin.Write([]byte{'!'})
				wantExit = 23
			} else {
				_ = owner.stdin.Close()
			}
			owner.wait(t, wantExit)
			// OS-confirmed terminal owner, not a PID/timeout inference.
			restarted := startOwnershipChild(t, dir)
			restarted.expect(t, "OWNED")
			_ = restarted.stdin.Close()
			restarted.wait(t, 0)
			names, err := os.ReadDir(dir)
			raw, readErr := os.ReadFile(filepath.Join(dir, "opaque"))
			if err != nil || readErr != nil || len(names) != 1 || string(raw) != "untouched fixture" {
				t.Fatal("process ownership changed fixture content or left lock files")
			}
		})
	}
}
