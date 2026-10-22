//go:build windows

package runtime

import (
	"context"
	"net"
	"strconv"
	"syscall"
)

// WinSock2.h defines SO_EXCLUSIVEADDRUSE as (int)(~SO_REUSEADDR).
// Python sets it before bind; Go's default merely avoids SO_REUSEADDR.
const exclusiveAddressUse = ^syscall.SO_REUSEADDR

func listenLoopback(ctx context.Context) (net.Listener, error) {
	return listenLoopbackPort(ctx, 0)
}

func listenLoopbackPort(ctx context.Context, port int) (net.Listener, error) {
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var optionError error
		if err := raw.Control(func(socket uintptr) {
			optionError = syscall.SetsockoptInt(syscall.Handle(socket), syscall.SOL_SOCKET, exclusiveAddressUse, 1)
		}); err != nil {
			return err
		}
		return optionError
	}}
	return config.Listen(ctx, "tcp4", "127.0.0.1:"+strconv.Itoa(port))
}
