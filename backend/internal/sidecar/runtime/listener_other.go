//go:build !windows

package runtime

import (
	"context"
	"net"
	"strconv"
)

func listenLoopback(ctx context.Context) (net.Listener, error) {
	return listenLoopbackPort(ctx, 0)
}

func listenLoopbackPort(ctx context.Context, port int) (net.Listener, error) {
	var config net.ListenConfig
	return config.Listen(ctx, "tcp4", "127.0.0.1:"+strconv.Itoa(port))
}
