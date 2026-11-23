package sshhost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// Upgrade is the protocol the VM's ProxyCommand asks the gateway to switch
// to: from then on the connection carries the bytes of the SSH session.
const Upgrade = "isotty-ssh"

const dialTimeout = 10 * time.Second

// Event is a connection the relay made or ended for the VM: Result is
// empty on open, and on close says how it ended.
type Event struct {
	Operation string // "open" or "close"
	Name      string
	Address   string
	Sent      int64 // bytes from the VM to the machine
	Received  int64 // bytes from the machine to the VM
	Seconds   int
	Result    string
}

// Relay connects the VM to the machines of the pinned config, and only to
// them. It holds the connections it made, so that they end with the attach.
type Relay struct {
	cfg  ProjectConfig
	dial func(ctx context.Context, network, address string) (net.Conn, error)

	mu     sync.Mutex
	open   map[net.Conn]struct{}
	closed bool
}

func NewRelay(cfg ProjectConfig) *Relay {
	dialer := &net.Dialer{Timeout: dialTimeout}
	return &Relay{cfg: cfg, dial: dialer.DialContext, open: map[net.Conn]struct{}{}}
}

// Close ends every connection the relay holds; the attach calls it as it
// ends, since a relayed connection does not belong to the gateway's server.
func (r *Relay) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for conn := range r.open {
		conn.Close()
	}
}

func (r *Relay) hold(conns ...net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	for _, conn := range conns {
		r.open[conn] = struct{}{}
	}
	return true
}

func (r *Relay) ended() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *Relay) release(conns ...net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, conn := range conns {
		delete(r.open, conn)
	}
}

// Handler answers GET /<name> with an upgrade to the machine's SSH port.
// onEvent runs outside the request, in order for each connection: the
// open is recorded while the session runs, so it does not delay the login.
func Handler(relay *Relay, onEvent func(Event)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{name}", func(w http.ResponseWriter, req *http.Request) {
		name := req.PathValue("name")
		h, ok := relay.cfg.Hosts[name]
		if !ok {
			gateway.WriteError(w, http.StatusNotFound, fmt.Sprintf("%s is not a machine this project may log in to; the human adds one with `isotty host ssh add` on the host", name))
			return
		}
		if !strings.EqualFold(req.Header.Get("Upgrade"), Upgrade) {
			gateway.WriteError(w, http.StatusBadRequest, "the relay carries an SSH session; use `ssh "+name+"`, whose ProxyCommand asks for it")
			return
		}
		if relay.ended() {
			gateway.WriteError(w, http.StatusServiceUnavailable, "the attach is ending")
			return
		}
		address := net.JoinHostPort(h.Host, strconv.Itoa(h.Port))
		remote, err := relay.dial(req.Context(), "tcp", address)
		if err != nil {
			gateway.WriteError(w, http.StatusBadGateway, fmt.Sprintf("the host could not reach %s: %v", address, err))
			go onEvent(Event{Operation: "close", Name: name, Address: h.Address(), Result: err.Error()})
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			remote.Close()
			gateway.WriteError(w, http.StatusInternalServerError, "the gateway cannot hand over the connection")
			return
		}
		local, buffered, err := hijacker.Hijack()
		if err != nil {
			remote.Close()
			return
		}
		// The server's header deadline would end the session.
		if err := local.SetDeadline(time.Time{}); err != nil {
			local.Close()
			remote.Close()
			return
		}
		if !relay.hold(local, remote) {
			local.Close()
			remote.Close()
			return
		}
		started := time.Now()
		opened := make(chan struct{})
		go func() {
			onEvent(Event{Operation: "open", Name: name, Address: h.Address()})
			close(opened)
		}()
		go func() {
			sent, received, result := splice(local, buffered, remote)
			relay.release(local, remote)
			<-opened
			onEvent(Event{Operation: "close", Name: name, Address: h.Address(), Sent: sent, Received: received, Seconds: int(time.Since(started).Seconds()), Result: result})
		}()
	})
	return mux
}

// splice answers the upgrade and copies both ways until both sides are
// done; bytes the VM sent after its request are already in buffered.
func splice(local net.Conn, buffered *bufio.ReadWriter, remote net.Conn) (sent, received int64, result string) {
	defer local.Close()
	defer remote.Close()
	if _, err := io.WriteString(local, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+Upgrade+"\r\n\r\n"); err != nil {
		return 0, 0, err.Error()
	}
	var wg sync.WaitGroup
	var toMachine error
	wg.Add(1)
	go func() {
		defer wg.Done()
		sent, toMachine = io.Copy(remote, buffered.Reader)
		closeWrite(remote)
	}()
	var toVM error
	received, toVM = io.Copy(local, remote)
	closeWrite(local)
	wg.Wait()
	for _, err := range []error{toMachine, toVM} {
		if errors.Is(err, net.ErrClosed) {
			return sent, received, "ended with the attach"
		}
		if err != nil {
			return sent, received, err.Error()
		}
	}
	return sent, received, "ok"
}

func closeWrite(conn net.Conn) {
	if half, ok := conn.(interface{ CloseWrite() error }); ok {
		half.CloseWrite()
		return
	}
	conn.Close()
}
