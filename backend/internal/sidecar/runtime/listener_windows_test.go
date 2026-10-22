//go:build windows

package runtime

import (
	"context"
	"net"
	"syscall"
	"testing"
)

func TestWindowsListenerIsExclusiveBeforeBinding(t *testing.T) {
	listener, err := listenLoopback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	tcp, ok := listener.(*net.TCPListener)
	if !ok {
		t.Fatal("listener is not TCP")
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var value int
	var optionError error
	if err := raw.Control(func(socket uintptr) {
		value, optionError = syscall.GetsockoptInt(syscall.Handle(socket), syscall.SOL_SOCKET, exclusiveAddressUse)
	}); err != nil {
		t.Fatal(err)
	}
	if optionError != nil || value != 1 {
		t.Fatalf("SO_EXCLUSIVEADDRUSE = %d, %v", value, optionError)
	}
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var optionError error
		if err := raw.Control(func(socket uintptr) {
			optionError = syscall.SetsockoptInt(syscall.Handle(socket), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		}); err != nil {
			return err
		}
		return optionError
	}}
	duplicate, err := config.Listen(context.Background(), "tcp4", listener.Addr().String())
	if duplicate != nil {
		duplicate.Close()
	}
	if err == nil {
		t.Fatal("reuse-address socket bound the owned listener's address")
	}
}
