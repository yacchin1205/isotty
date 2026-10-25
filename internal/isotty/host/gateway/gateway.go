// Package gateway serves host-side services to the VM for the duration of an
// attach session. The server listens on a host loopback port and `ssh -R`
// exposes it on the VM's loopback at VMPort; each service mounts under its
// own path prefix.
package gateway

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// VMPort is where the VM reaches the gateway. It sits above the Linux
// ephemeral range so it collides neither with services a project runs in the
// VM nor with outgoing connections.
const VMPort = 61001

type Gateway struct {
	listener net.Listener
	server   *http.Server
	served   chan error
}

func Serve(handler http.Handler) (*Gateway, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for host gateway: %w", err)
	}
	gateway := &Gateway{
		listener: listener,
		// The VM is not trusted; a connection that never sends its headers
		// must not hold a goroutine for the rest of the session.
		server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second},
		served: make(chan error, 1),
	}
	go func() {
		gateway.served <- gateway.server.Serve(listener)
	}()
	return gateway, nil
}

func (g *Gateway) Port() int {
	return g.listener.Addr().(*net.TCPAddr).Port
}

func (g *Gateway) Close() error {
	if err := g.server.Close(); err != nil {
		return fmt.Errorf("close host gateway: %w", err)
	}
	if err := <-g.served; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("host gateway: %w", err)
	}
	return nil
}
