package isotty

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host"
	"github.com/yazawa/isotty/internal/isotty/host/browser"
	"github.com/yazawa/isotty/internal/isotty/host/docker"
	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	"github.com/yazawa/isotty/internal/isotty/host/sshhost"
	"github.com/yazawa/isotty/internal/isotty/host/wake"
	"github.com/yazawa/isotty/internal/isotty/opeco"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

// The daemon holds, for every project of the host user whose VM runs, the
// connection that carries its forwards and its gateway, apart from any
// terminal; see docs/FORWARD.md. It also serves the wakes.

// daemonPoll is how often the daemon looks again at a VM that is not
// running, at the projects there are, and at the wakes due.
const daemonPoll = 30 * time.Second

// daemonRetry is how long a dropped connection waits before the next one.
const daemonRetry = 5 * time.Second

func daemonSocketPath(homeDir string) string { return filepath.Join(homeDir, "daemon.sock") }
func daemonLogPath(homeDir string) string    { return filepath.Join(homeDir, "daemon.log") }

func (a *App) runDaemon(args []string) error {
	if len(args) == 0 {
		return errors.New("daemon requires a subcommand: status, restart, stop or run")
	}
	fs := flag.NewFlagSet("daemon "+args[0], flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("daemon %s does not accept arguments", args[0])
	}
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	switch args[0] {
	case "run":
		return a.serveDaemon(homeDir)
	case "status":
		statuses, err := daemonStatus(homeDir)
		if err != nil {
			return err
		}
		a.printDaemonStatus(statuses)
		return nil
	case "stop":
		return a.stopDaemon(homeDir)
	case "restart":
		if err := a.stopDaemon(homeDir); err != nil {
			return err
		}
		return a.ensureDaemon(homeDir)
	}
	return fmt.Errorf("unknown daemon subcommand %q", args[0])
}

// holdStatus is how the daemon holds one project, for `daemon status` and
// for the attach's line.
type holdStatus struct {
	ProjectHash string    `json:"project_hash"`
	ProjectPath string    `json:"project_path"`
	VM          string    `json:"vm"`
	Gateway     string    `json:"gateway"`
	Services    []string  `json:"services,omitempty"`
	Refused     []string  `json:"refused,omitempty"`
	Forwards    []string  `json:"forwards,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Since       time.Time `json:"since"`
}

type daemon struct {
	a       *App
	homeDir string
	mu      sync.Mutex
	holds   map[string]*hold
	stop    chan struct{}
	logMu   sync.Mutex
	// binary is the digest of the binary the daemon runs, which a newer
	// command replaces it for.
	binary string
	// running counts the holds, which the daemon waits for as it stops:
	// their ssh would otherwise outlive it and keep holding the VM's port.
	running sync.WaitGroup
}

type hold struct {
	d      *daemon
	hash   string
	cancel context.CancelFunc
	reload chan struct{}
	mu     sync.Mutex
	status holdStatus
	// read is the project as the daemon last read it, which a connection
	// after a drop serves again: what the host serves changes at `up`,
	// which asks for a reading, not whenever the network comes back.
	read *hostConfig
	// stale says a reading was asked for while the VM was not connected.
	stale atomic.Bool
}

func (a *App) serveDaemon(homeDir string) error {
	lock, err := lockDaemon(filepath.Join(homeDir, "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	socket := daemonSocketPath(homeDir)
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socket, err)
	}
	// The socket stops and steers the daemon: the host user's alone.
	if err := os.Chmod(socket, 0o600); err != nil {
		return errors.Join(err, listener.Close())
	}
	binary, err := executableDigest()
	if err != nil {
		return errors.Join(err, listener.Close())
	}
	d := &daemon{a: a, homeDir: homeDir, holds: map[string]*hold{}, stop: make(chan struct{}), binary: binary}
	server := &http.Server{Handler: d.control(), ReadHeaderTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	d.log("", "daemon started (pid %d)", os.Getpid())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			d.halt()
		case <-d.stop:
		}
	}()
	go d.serveWakes()
	for {
		if err := d.syncHolds(); err != nil {
			d.log("", "list projects: %v", err)
		}
		select {
		case <-d.stop:
			d.mu.Lock()
			for _, h := range d.holds {
				h.cancel()
			}
			d.mu.Unlock()
			d.running.Wait()
			d.log("", "daemon stopped")
			return server.Close()
		case err := <-served:
			return fmt.Errorf("control socket: %w", err)
		case <-time.After(daemonPoll):
		}
	}
}

func (d *daemon) log(project, format string, args ...any) {
	d.logMu.Lock()
	defer d.logMu.Unlock()
	prefix := time.Now().Format(time.RFC3339)
	if project != "" {
		prefix += " " + project
	}
	fmt.Fprintf(d.a.stdout, "%s %s\n", prefix, fmt.Sprintf(format, args...))
}

// syncHolds holds every project with local state, and lets go of those
// whose state `down` removed.
func (d *daemon) syncHolds() error {
	states, err := ListStates()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, state := range states {
		seen[state.ProjectHash] = true
		d.ensureHold(state.ProjectHash)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for hash, h := range d.holds {
		if !seen[hash] {
			h.cancel()
			delete(d.holds, hash)
		}
	}
	return nil
}

func (d *daemon) ensureHold(hash string) *hold {
	d.mu.Lock()
	defer d.mu.Unlock()
	if h, ok := d.holds[hash]; ok {
		return h
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &hold{d: d, hash: hash, cancel: cancel, reload: make(chan struct{}, 1), status: holdStatus{ProjectHash: hash, Gateway: "starting", Since: time.Now()}}
	d.holds[hash] = h
	d.running.Add(1)
	go func() {
		defer d.running.Done()
		h.run(ctx)
		d.mu.Lock()
		if d.holds[hash] == h {
			delete(d.holds, hash)
		}
		d.mu.Unlock()
	}()
	return h
}

func (h *hold) set(fn func(*holdStatus)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.status.Gateway
	fn(&h.status)
	if h.status.Gateway != before {
		h.status.Since = time.Now()
		h.d.log(h.status.ProjectPath, "gateway %s", h.status.Gateway)
	}
}

func (h *hold) snapshot() holdStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

// wait returns after the poll, at a reload, or when the hold ends.
func (h *hold) wait(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-h.reload:
		return true
	case <-time.After(d):
		return true
	}
}

// run holds the project until its state is gone or the daemon stops.
func (h *hold) run(ctx context.Context) {
	for {
		state, err := LoadState(h.hash)
		if err != nil {
			if !os.IsNotExist(err) {
				h.set(func(s *holdStatus) { s.LastError = err.Error() })
				if h.wait(ctx, daemonPoll) {
					continue
				}
			}
			return
		}
		h.set(func(s *holdStatus) { s.ProjectPath = state.ProjectPath })
		status, err := vmcfg.GCPInstanceStatus(state.GCPProjectID, state.Zone, state.InstanceName)
		if err != nil {
			h.set(func(s *holdStatus) { s.LastError = "check the VM: " + err.Error() })
		} else {
			h.set(func(s *holdStatus) { s.VM = status })
		}
		if err != nil || status != vmcfg.GCPStatusRunning {
			if err == nil {
				h.set(func(s *holdStatus) { s.Gateway = "waiting for the VM to run" })
			}
			if !h.wait(ctx, daemonPoll) {
				return
			}
			continue
		}
		if err := h.connect(ctx, state); err != nil {
			h.set(func(s *holdStatus) { s.Gateway = "dropped"; s.LastError = err.Error() })
		}
		if ctx.Err() != nil {
			return
		}
		if !h.wait(ctx, daemonRetry) {
			return
		}
	}
}

// swappable is the gateway's handler, replaced at a reload without
// dropping the connection.
type swappable struct{ handler atomic.Pointer[http.Handler] }

func (s *swappable) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.handler.Load()).ServeHTTP(w, r)
}

// connect holds the project's connection while the VM runs, until it drops
// or the hold ends: the gateway's reverse forward on one ssh, the forwards
// on another, so a local port that is taken costs only its forward.
func (h *hold) connect(ctx context.Context, state State) error {
	conn := vmcfg.GCPConnection{ProjectID: state.GCPProjectID, Zone: state.Zone, InstanceName: state.InstanceName, SSHConfigPath: state.SSHConfigPath}
	target := h.d.a.auditTarget(conn)
	record := func(record runtimecfg.VMEventRecord) error { return runtimecfg.RecordVMEvent(target, record, false) }
	fail := func(err error) {
		h.set(func(s *holdStatus) { s.LastError = err.Error() })
		h.d.log(state.ProjectPath, "%v", err)
	}
	if err := vmcfg.RefreshGCPSSHConfig(conn, false); err != nil {
		return fmt.Errorf("refresh SSH config: %w", err)
	}
	workspacePath, err := vmcfg.GetGCPWorkspacePath(conn)
	if err != nil {
		return err
	}
	files := &browser.Files{Workspace: workspacePath, Project: state.ProjectPath, Flush: func() error { return flushMutagenSession(state) }, Ignored: syncIgnored}
	if err := files.Serve(); err != nil {
		return err
	}
	defer files.Close()

	gw := &swappable{}
	if h.read == nil || h.stale.Swap(false) {
		read := loadHostConfig(state.ProjectPath, state, h.d.homeDir)
		h.read = &read
	}
	cfg := *h.read
	relay, err := h.serve(cfg, state, conn, files, gw, record, fail)
	if err != nil {
		return err
	}
	defer func() { relay.Close() }()
	server, err := gateway.Serve(gw)
	if err != nil {
		return err
	}
	defer server.Close()

	// A connection that dropped holds the VM port until sshd notices.
	h.set(func(s *holdStatus) { s.Gateway = "waiting for the VM port" })
	if err := target.Run(gateway.PortFreeCommand(gateway.PortWait), false); err != nil {
		if target.ExitCode(err) == gateway.PortHeld {
			return fmt.Errorf("VM port %d is held by another connection", gateway.VMPort)
		}
		return err
	}

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The report ends once the sync is done; an `up`, which starts the
	// sync session again, asks for a reading, and the report starts again.
	stopSync := make(chan struct{})
	var syncing sync.WaitGroup
	var syncRunning atomic.Bool
	startSync := func() {
		if !syncRunning.CompareAndSwap(false, true) {
			return
		}
		syncing.Add(1)
		go func() {
			defer syncing.Done()
			defer syncRunning.Store(false)
			if err := reportSync(state, conn, stopSync); err != nil {
				fail(err)
			}
		}()
	}
	startSync()
	defer func() {
		close(stopSync)
		syncing.Wait()
	}()
	stopSweep := h.sweep(cfg, state, fail)
	defer func() { stopSweep() }()

	var forwardsRunning sync.WaitGroup
	defer forwardsRunning.Wait()
	startForwards := func(cfg host.ForwardConfig) context.CancelFunc {
		ctx, stop := context.WithCancel(connCtx)
		forwardsRunning.Add(1)
		go func() {
			defer forwardsRunning.Done()
			h.holdForwards(ctx, conn, cfg)
		}()
		return stop
	}
	stopForwards := startForwards(cfg.forwards)

	gatewayArgs := daemonSSHArgs(conn, "-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", gateway.VMPort, server.Port()))
	gatewayDone := make(chan error, 1)
	go func() { gatewayDone <- runDaemonSSH(connCtx, gatewayArgs, nil) }()
	// Every way out ends the ssh and waits for it.
	gatewayEnded := false
	defer func() {
		cancel()
		if !gatewayEnded {
			<-gatewayDone
		}
	}()
	if event, err := runtimecfg.NewAttachVMEvent(h.hash, "host-start", len(cfg.forwards.Forwards), ""); err == nil {
		if err := record(event); err != nil {
			fail(fmt.Errorf("record host-start event: %w", err))
		}
	}
	h.set(func(s *holdStatus) { s.Gateway = "connected"; s.LastError = "" })
	for {
		select {
		case err := <-gatewayDone:
			gatewayEnded = true
			stopForwards()
			return h.ended(ctx, target, len(cfg.forwards.Forwards), err)
		case <-h.reload:
			if _, err := LoadState(h.hash); os.IsNotExist(err) {
				// `down` removed the project.
				stopForwards()
				return nil
			}
			h.stale.Store(false)
			read := loadHostConfig(state.ProjectPath, state, h.d.homeDir)
			h.read = &read
			next := read
			nextRelay, err := h.serve(next, state, conn, files, gw, record, fail)
			if err != nil {
				fail(fmt.Errorf("read the project again: %w", err))
				continue
			}
			startSync()
			relay.Close()
			relay = nextRelay
			if !sameForwards(cfg.forwards, next.forwards) {
				stopForwards()
				stopForwards = startForwards(next.forwards)
			}
			stopSweep()
			stopSweep = h.sweep(next, state, fail)
			cfg = next
			h.d.log(state.ProjectPath, "read the project again")
		case <-ctx.Done():
			stopForwards()
			return nil
		}
	}
}

// ended records the connection's end, while the VM can take the record.
func (h *hold) ended(ctx context.Context, target runtimecfg.RemoteTarget, forwards int, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	result := "dropped"
	if err != nil {
		result = err.Error()
	}
	event, eventErr := runtimecfg.NewAttachVMEvent(h.hash, "host-end", forwards, result)
	if eventErr == nil {
		eventErr = runtimecfg.RecordVMEvent(target, event, false)
	}
	if eventErr != nil {
		// A VM that powered off takes no record; its idle-stop is there.
		h.d.log(h.snapshot().ProjectPath, "record host-end event: %v", eventErr)
	}
	return fmt.Errorf("the connection ended: %s", result)
}

// serve puts the project's relays, as read, behind the gateway.
func (h *hold) serve(cfg hostConfig, state State, conn vmcfg.GCPConnection, files *browser.Files, gw *swappable, record func(runtimecfg.VMEventRecord) error, fail func(error)) (*sshRelayCloser, error) {
	relay := &sshRelayCloser{}
	if cfg.ssh != nil {
		relay.relay = sshhost.NewRelay(*cfg.ssh)
	}
	handler, services, err := h.d.a.gatewayHandler(conn, state.ProjectHash, files, cfg.epweave, cfg.github, cfg.gitlab, cfg.bitbucket, cfg.redmine, cfg.docker, cfg.google, cfg.aws, cfg.gas, cfg.dns, cfg.child, relay.relay, record, fail)
	if err != nil {
		relay.Close()
		return nil, err
	}
	gw.handler.Store(&handler)
	var refused []string
	if text := cfg.describeRefused(); text != "" {
		refused = strings.Split(text, "\n")
		for _, line := range refused {
			h.d.log(state.ProjectPath, "%s", line)
		}
	}
	h.set(func(s *holdStatus) { s.Services = services; s.Refused = refused })
	return relay, nil
}

type sshRelayCloser struct{ relay *sshhost.Relay }

func (c *sshRelayCloser) Close() {
	if c != nil && c.relay != nil {
		c.relay.Close()
	}
}

// sweep keeps the project's Docker cache swept while the VM runs.
func (h *hold) sweep(cfg hostConfig, state State, fail func(error)) func() {
	if cfg.docker == nil {
		return func() {}
	}
	dockerHost, err := docker.LoadHostConfig(h.d.homeDir)
	if err != nil {
		fail(err)
		return func() {}
	}
	return docker.CacheFor(state.ProjectHash).KeepSwept(docker.CLI{}, dockerHost.Keep, fail)
}

// holdForwards keeps the project's forwards up while the connection lasts.
// ssh goes on with the forwards it could bind; the ones it could not are
// told on its stderr, and reported.
func (h *hold) holdForwards(ctx context.Context, conn vmcfg.GCPConnection, cfg host.ForwardConfig) {
	names := host.SortedForwardNames(cfg)
	if len(names) == 0 {
		h.set(func(s *holdStatus) { s.Forwards = nil })
		return
	}
	var forwardArgs []string
	for _, name := range names {
		forward := cfg.Forwards[name]
		forwardArgs = append(forwardArgs, "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", forward.LocalPort, forward.RemotePort))
	}
	for ctx.Err() == nil {
		h.set(func(s *holdStatus) {
			s.Forwards = nil
			for _, name := range names {
				f := cfg.Forwards[name]
				s.Forwards = append(s.Forwards, fmt.Sprintf("%s localhost:%d -> %d", name, f.LocalPort, f.RemotePort))
			}
		})
		args := daemonSSHArgs(conn, forwardArgs...)
		args = append([]string{"-o", "ExitOnForwardFailure=no"}, args...)
		err := runDaemonSSH(ctx, args, func(line string) {
			if strings.Contains(line, "forwarding") || strings.Contains(line, "bind") {
				h.set(func(s *holdStatus) { s.Forwards = append(s.Forwards, "not up: "+line) })
				h.d.log(h.snapshot().ProjectPath, "forward: %s", line)
			}
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			h.d.log(h.snapshot().ProjectPath, "forwards: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(daemonRetry):
		}
	}
}

func sameForwards(a, b host.ForwardConfig) bool {
	if len(a.Forwards) != len(b.Forwards) {
		return false
	}
	for name, f := range a.Forwards {
		if b.Forwards[name] != f {
			return false
		}
	}
	return true
}

// daemonSSHArgs holds a connection with no terminal: a dead peer is noticed
// within ~45s, and a forward of the gateway that cannot bind ends it.
func daemonSSHArgs(conn vmcfg.GCPConnection, forwards ...string) []string {
	args := []string{
		"-F", conn.SSHConfigPath, "-T",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
	}
	args = append(args, forwards...)
	return append(args, fmt.Sprintf("%s.%s.%s", conn.InstanceName, conn.Zone, conn.ProjectID), "exec sleep infinity")
}

// runDaemonSSH runs ssh until it ends or ctx does, giving each line of its
// stderr to line.
func runDaemonSSH(ctx context.Context, args []string, line func(string)) error {
	cmd := exec.CommandContext(ctx, "ssh", args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var last string
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		last = scanner.Text()
		if line != nil {
			line(last)
		}
	}
	err = cmd.Wait()
	if err != nil && last != "" {
		return fmt.Errorf("%w: %s", err, last)
	}
	return err
}

// serveWakes delivers the due wakes of the projects that allow them.
func (d *daemon) serveWakes() {
	var mu sync.Mutex
	busy := map[string]bool{}
	for {
		select {
		case <-d.stop:
			return
		case <-time.After(daemonPoll):
		}
		consents, err := wake.Consents(d.homeDir)
		if err != nil {
			d.log("", "read wake consents: %v", err)
			continue
		}
		for _, consent := range consents {
			requests, err := wake.Load(d.homeDir, consent.ProjectHash)
			if err != nil {
				d.log(consent.ProjectPath, "read wakes: %v", err)
				continue
			}
			mu.Lock()
			skip := busy[consent.ProjectHash]
			mu.Unlock()
			if skip || len(requests) == 0 || requests[0].At.After(time.Now()) {
				continue
			}
			mu.Lock()
			busy[consent.ProjectHash] = true
			mu.Unlock()
			go func(consent wake.Consent, request wake.Request) {
				err := d.wake(consent, request)
				var recordErr wakeRecordError
				switch {
				case errors.As(err, &recordErr):
					d.log(consent.ProjectPath, "wake %s: %v", request.ID, err)
				case err != nil:
					d.wakeFailed(consent, request, err)
				}
				mu.Lock()
				defer mu.Unlock()
				busy[consent.ProjectHash] = false
			}(consent, requests[0])
		}
	}
}

// wakeFailed tells the human, on the card of the VM's opeco session when
// the VM can be reached, and in the log either way. A one-time wake is
// tried again later, and given up after its last try, so that one that
// never can be delivered does not start the VM for ever; a recurring one
// goes on to its next time, so that one failure does not end it.
func (d *daemon) wakeFailed(consent wake.Consent, request wake.Request, err error) {
	next := fmt.Sprintf("trying again in %d minutes", int(wakeRetry.Minutes()))
	var moveErr error
	if request.Cron != "" {
		next = "skipped until its next time"
		moveErr = wake.Done(d.homeDir, consent.ProjectHash, request, time.Now())
	} else {
		var dropped bool
		dropped, moveErr = wake.Failed(d.homeDir, consent.ProjectHash, request.ID, time.Now().Add(wakeRetry))
		if dropped {
			next = fmt.Sprintf("given up after %d tries", wake.MaxFailures)
		}
	}
	if moveErr != nil {
		d.log(consent.ProjectPath, "move wake %s on: %v", request.ID, moveErr)
	}
	message := fmt.Sprintf("isotty could not wake the conversation for %q (%v); %s", request.Prompt, err, next)
	d.log(consent.ProjectPath, "wake %s: %s", request.ID, message)
	state, stateErr := LoadState(consent.ProjectHash)
	if stateErr != nil {
		return
	}
	conn := vmcfg.GCPConnection{ProjectID: state.GCPProjectID, Zone: state.Zone, InstanceName: state.InstanceName, SSHConfigPath: state.SSHConfigPath}
	if notifyErr := d.a.auditTarget(conn).Run(opeco.SessionCommand("notify", message), false); notifyErr != nil {
		d.log(consent.ProjectPath, "the VM could not tell the human either: %v", notifyErr)
	}
}

// wake starts the VM for a due wake when it is off, waits for its gateway,
// and gives the conversation the prompt.
func (d *daemon) wake(consent wake.Consent, request wake.Request) error {
	state, err := LoadState(consent.ProjectHash)
	if err != nil {
		return err
	}
	status, err := vmcfg.GCPInstanceStatus(state.GCPProjectID, state.Zone, state.InstanceName)
	if err != nil {
		return err
	}
	if status != vmcfg.GCPStatusRunning {
		d.log(consent.ProjectPath, "starting %s for wake %s", state.InstanceName, request.ID)
		if err := d.a.runUp([]string{"--no-attach", consent.ProjectPath}); err != nil {
			return fmt.Errorf("start the VM: %w", err)
		}
	}
	h := d.ensureHold(consent.ProjectHash)
	deadline := time.Now().Add(5 * time.Minute)
	for h.snapshot().Gateway != "connected" {
		if time.Now().After(deadline) {
			return fmt.Errorf("the gateway was not connected within five minutes: %s", h.snapshot().LastError)
		}
		select {
		case h.reload <- struct{}{}:
		default:
		}
		time.Sleep(5 * time.Second)
	}
	conn := vmcfg.GCPConnection{ProjectID: state.GCPProjectID, Zone: state.Zone, InstanceName: state.InstanceName, SSHConfigPath: state.SSHConfigPath}
	return d.a.deliverWake(d.a.auditTarget(conn), consent.ProjectHash, d.homeDir, request)
}

// halt stops the daemon, once.
func (d *daemon) halt() {
	d.mu.Lock()
	defer d.mu.Unlock()
	select {
	case <-d.stop:
	default:
		close(d.stop)
	}
}

// control serves the daemon's socket.
func (d *daemon) control() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /binary", func(w http.ResponseWriter, r *http.Request) {
		gateway.WriteJSON(w, http.StatusOK, map[string]string{"sha256": d.binary})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		statuses := make([]holdStatus, 0, len(d.holds))
		for _, h := range d.holds {
			statuses = append(statuses, h.snapshot())
		}
		d.mu.Unlock()
		sort.Slice(statuses, func(i, j int) bool { return statuses[i].ProjectPath < statuses[j].ProjectPath })
		gateway.WriteJSON(w, http.StatusOK, statuses)
	})
	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, r *http.Request) {
		h := d.ensureHold(r.URL.Query().Get("project"))
		h.stale.Store(true)
		select {
		case h.reload <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go d.halt()
	})
	return mux
}

func daemonClient(homeDir string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", daemonSocketPath(homeDir))
		}},
	}
}

func daemonCall(homeDir, method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, "http://isotty-daemon"+path, nil)
	if err != nil {
		return nil, err
	}
	return daemonClient(homeDir).Do(req)
}

func daemonStatus(homeDir string) ([]holdStatus, error) {
	resp, err := daemonCall(homeDir, http.MethodGet, "/status")
	if err != nil {
		return nil, fmt.Errorf("the isotty daemon is not running: %w", err)
	}
	defer resp.Body.Close()
	var statuses []holdStatus
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return nil, err
	}
	return statuses, nil
}

// reloadProject has the daemon read the project again, and hold it from
// now on if it did not.
func reloadProject(homeDir, projectHash string) error {
	resp, err := daemonCall(homeDir, http.MethodPost, "/reload?project="+projectHash)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("the daemon answered %s", resp.Status)
	}
	return nil
}

// ensureDaemon starts the daemon when it is not running: on a Mac as the
// launchd agent, which starts it at login from then on; elsewhere as a
// process of its own, as mutagen's commands start mutagen's daemon.
func (a *App) ensureDaemon(homeDir string) error {
	if err := a.replaceOlderDaemon(homeDir); err != nil {
		return err
	}
	if launchdAvailable() {
		if err := a.installLaunchd(homeDir); err != nil {
			return err
		}
	} else if _, err := daemonStatus(homeDir); err == nil {
		return nil
	} else if err := spawnDaemon(homeDir); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, err := daemonStatus(homeDir)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w; see %s", err, daemonLogPath(homeDir))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// replaceOlderDaemon stops a daemon that runs another binary than this
// command's, a new isotty in the same place among them, so the start that
// follows runs this one: a binary replaced in place changes nothing
// launchd or a running process would notice.
func (a *App) replaceOlderDaemon(homeDir string) error {
	resp, err := daemonCall(homeDir, http.MethodGet, "/binary")
	if err != nil {
		return nil // not running
	}
	defer resp.Body.Close()
	var running struct {
		SHA256 string `json:"sha256"`
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&running); err != nil {
			return err
		}
	}
	ours, err := executableDigest()
	if err != nil {
		return err
	}
	if running.SHA256 == ours {
		return nil
	}
	a.phase("Replacing the isotty daemon, which runs another binary")
	return a.stopDaemon(homeDir)
}

// executableDigest names the binary this process runs.
func executableDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (a *App) stopDaemon(homeDir string) error {
	if launchdAvailable() {
		// launchd would start it again at once.
		return a.removeLaunchd()
	}
	if _, err := daemonStatus(homeDir); err != nil {
		return nil // not running
	}
	resp, err := daemonCall(homeDir, http.MethodPost, "/stop")
	if err != nil {
		return err
	}
	resp.Body.Close()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := daemonStatus(homeDir); err != nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("the daemon did not stop within 15 seconds")
}

func spawnDaemon(homeDir string) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(daemonLogPath(homeDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(binary, "daemon", "run")
	cmd.Stdin = nil
	// Not the project directory of whatever command started it.
	cmd.Dir = "/"
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the isotty daemon: %w", err)
	}
	return cmd.Process.Release()
}

func (a *App) printDaemonStatus(statuses []holdStatus) {
	if len(statuses) == 0 {
		fmt.Fprintln(a.stdout, "The daemon holds no project.")
	}
	for _, s := range statuses {
		fmt.Fprintf(a.stdout, "%s\n  VM: %s\n  gateway: %s since %s\n", s.ProjectPath, s.VM, s.Gateway, s.Since.Local().Format("15:04:05"))
		if len(s.Services) > 0 {
			fmt.Fprintf(a.stdout, "  serves: %s\n", strings.Join(s.Services, ", "))
		}
		for _, f := range s.Forwards {
			fmt.Fprintf(a.stdout, "  forward: %s\n", f)
		}
		for _, r := range s.Refused {
			fmt.Fprintf(a.stdout, "  %s\n", r)
		}
		if s.LastError != "" {
			fmt.Fprintf(a.stdout, "  last error: %s\n", s.LastError)
		}
	}
}
