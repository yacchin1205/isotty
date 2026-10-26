package isotty

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host"
	"github.com/yazawa/isotty/internal/isotty/host/epweave"
	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	"github.com/yazawa/isotty/internal/isotty/host/github"
	"github.com/yazawa/isotty/internal/isotty/host/google"
	"github.com/yazawa/isotty/internal/isotty/opeco"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

const (
	defaultSyncMode    = "two-way-safe"
	oneWaySafeSyncMode = "one-way-safe"
	twoWaySafeSyncMode = "two-way-safe"
)

var supportedSyncModes = map[string]struct{}{
	oneWaySafeSyncMode: {},
	twoWaySafeSyncMode: {},
}

type App struct {
	stdout io.Writer
	stderr io.Writer
	debug  bool
}

func NewApp() *App {
	return &App{
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		a.printUsage()
		return nil
	}
	if args[0] == "--debug" {
		a.debug = true
		args = args[1:]
		if len(args) == 0 {
			a.printUsage()
			return nil
		}
	}

	switch args[0] {
	case "up":
		return a.runUp(args[1:])
	case "id":
		return a.runID(args[1:])
	case "attach":
		return a.runAttach(args[1:])
	case "host":
		return a.runHost(args[1:])
	case "runtime":
		return a.runRuntime(args[1:])
	case "vm":
		return a.runVM(args[1:])
	case "audit":
		return a.runAudit(args[1:])
	case "down":
		return a.runDown(args[1:])
	case "status":
		return a.runStatus(args[1:])
	case "version":
		return a.runVersion(args[1:])
	case "help", "-h", "--help":
		a.printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) printUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  isotty [--debug] up [PATH] [--sync one-way-safe|two-way-safe] [--user <name>] [--no-attach]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] id")
	fmt.Fprintln(a.stdout, "  isotty [--debug] attach [--target <id>] [--user <name>] [--no-host]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] down [--force]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] audit logs [-f]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host forward add <name> --local-port <port> --remote-port <port>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host forward list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host forward remove <name>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host epweave enable --prefix <name>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host epweave disable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host epweave show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host github enable [--remote <name>[:read|write]]... [--repo <owner/name>[:read|write]]...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host github disable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host github show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host google enable [--key <service-account.json>] --scope <scope>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host google disable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host google show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime apt add <package>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime apt remove <package>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime apt list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime service enable <name>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime service disable <name>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime service list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime post-install path")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime node set <major>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime node show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime agent add <name>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime agent remove <name>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime agent list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime tools enable <name>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime tools disable <name>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime tools list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime tools available")
	fmt.Fprintln(a.stdout, "  isotty [--debug] vm gcp show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] vm gcp set [--machine-type <type>] [--boot-disk-size <size>] [--image-family <family>] [--image-project <project>]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] status")
	fmt.Fprintln(a.stdout, "  isotty [--debug] version")
}

func (a *App) runID(args []string) error {
	fs := flag.NewFlagSet("id", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("id does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	state, err := LoadStateForProject(projectPath)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, vmcfg.FormatGCPID(state.GCPProjectID, state.Zone, state.InstanceName))
	return nil
}

func (a *App) runUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	syncMode := fs.String("sync", defaultSyncMode, "synchronization mode")
	user := fs.String("user", "", "SSH username to use for auto-attach")
	noAttach := fs.Bool("no-attach", false, "do not attach after preparing the environment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateSyncMode(*syncMode); err != nil {
		return err
	}
	if *noAttach && *user != "" {
		return errors.New("up does not accept --user with --no-attach")
	}
	if fs.NArg() > 1 {
		return errors.New("up accepts at most one path argument")
	}

	projectPath := "."
	if fs.NArg() == 1 {
		projectPath = fs.Arg(0)
	}

	projectPath, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}

	if err := requireExecutable("gcloud"); err != nil {
		return err
	}
	if err := requireExecutable("ssh"); err != nil {
		return err
	}
	if err := requireExecutable("mutagen"); err != nil {
		return err
	}

	cfg, err := LoadConfig(projectPath)
	if err != nil {
		return err
	}

	state, err := a.ensureEnvironment(cfg, *syncMode)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "IsoTTY environment is ready.\n")
	fmt.Fprintf(a.stdout, "Instance: %s\n", state.InstanceName)
	fmt.Fprintf(a.stdout, "Project: %s\n", state.GCPProjectID)
	fmt.Fprintf(a.stdout, "Zone: %s\n", state.Zone)
	fmt.Fprintf(a.stdout, "Sync mode: %s\n", state.SyncMode)
	if *noAttach {
		return nil
	}
	return a.attachToState(projectPath, state, *user, false)
}

func (a *App) runAudit(args []string) error {
	if len(args) == 0 {
		return errors.New("audit requires a subcommand: logs")
	}

	switch args[0] {
	case "logs":
		return a.runAuditLogs(args[1:])
	default:
		return fmt.Errorf("unknown audit subcommand %q", args[0])
	}
}

func (a *App) runAuditLogs(args []string) error {
	fs := flag.NewFlagSet("audit logs", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	follow := fs.Bool("f", false, "follow logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("audit logs does not accept positional arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	state, err := LoadStateForProject(projectPath)
	if err != nil {
		return err
	}

	if *follow {
		return a.followAuditLogs(state)
	}

	events, err := runtimecfg.QueryAuditLogs(a.auditTarget(vmcfg.GCPConnection{
		ProjectID:    state.GCPProjectID,
		Zone:         state.Zone,
		InstanceName: state.InstanceName,
	}), "boot")
	if err != nil {
		return err
	}
	a.printAuditEvents(events)
	return nil
}

func (a *App) runAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	targetID := fs.String("target", "", "attach to a remote IsoTTY target id")
	user := fs.String("user", "", "SSH username to use for attach")
	noHost := fs.Bool("no-host", false, "attach without port forwards and without the host gateway")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("attach does not accept arguments")
	}
	if err := requireExecutable("ssh"); err != nil {
		return err
	}
	if *targetID != "" {
		return a.attachToTarget(*targetID, *user, *noHost)
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	state, err := LoadStateForProject(projectPath)
	if err != nil {
		if IsStateNotFoundError(err) {
			return fmt.Errorf("%w\nhint: use `isotty attach --target <id>` with an id from `isotty id`", err)
		}
		return err
	}

	return a.attachToState(projectPath, state, *user, *noHost)
}

func (a *App) attachToTarget(targetID, user string, noHost bool) error {
	target, err := vmcfg.ParseGCPID(targetID)
	if err != nil {
		return err
	}
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	conn := vmcfg.GCPConnection{
		ProjectID:     target.ProjectID,
		Zone:          target.Zone,
		InstanceName:  target.InstanceName,
		User:          user,
		SSHConfigPath: filepath.Join(homeDir, "ssh", "config"),
	}

	workspacePath, err := vmcfg.GetGCPWorkspacePath(conn)
	if err != nil {
		return err
	}
	projectHash, err := vmcfg.GetGCPProjectHash(conn)
	if err != nil {
		return err
	}

	forwardCfg := host.ForwardConfig{Forwards: map[string]host.Forward{}}
	var epweaveCfg *epweave.ProjectConfig
	var githubCfg *github.ProjectConfig
	var googleCfg *google.ProjectConfig
	var agents []string
	if !noHost {
		if data, err := fetchOptionalProjectFile(conn, ".isotty/forward.yaml"); err != nil {
			return err
		} else if data != nil {
			forwardCfg, err = host.ParseForwardConfig(data)
			if err != nil {
				return fmt.Errorf("parse remote forward config: %w", err)
			}
		}
		if data, err := fetchOptionalProjectFile(conn, ".isotty/epweave.yaml"); err != nil {
			return err
		} else if data != nil {
			synced, err := epweave.ParseProjectConfig(data)
			if err != nil {
				return fmt.Errorf("parse remote epweave config: %w", err)
			}
			verified, err := epweave.VerifyPinned(homeDir, projectHash, synced)
			if err != nil {
				return err
			}
			epweaveCfg = &verified
		}
		if data, err := fetchOptionalProjectFile(conn, ".isotty/github.yaml"); err != nil {
			return err
		} else if data != nil {
			synced, err := github.ParseProjectConfig(data)
			if err != nil {
				return fmt.Errorf("parse remote github config: %w", err)
			}
			verified, err := github.VerifyPinned(homeDir, projectHash, synced)
			if err != nil {
				return err
			}
			githubCfg = &verified
		}
		if data, err := fetchOptionalProjectFile(conn, ".isotty/google.yaml"); err != nil {
			return err
		} else if data != nil {
			synced, err := google.ParseProjectConfig(data)
			if err != nil {
				return fmt.Errorf("parse remote google config: %w", err)
			}
			verified, err := google.VerifyPinned(homeDir, projectHash, synced)
			if err != nil {
				return err
			}
			googleCfg = &verified
		}
		if data, err := fetchOptionalProjectFile(conn, ".isotty/agent.yaml"); err != nil {
			return err
		} else if data != nil {
			agents, err = runtimecfg.ParseAgents(data)
			if err != nil {
				return fmt.Errorf("parse remote agent config: %w", err)
			}
		}
		if opeco.Active(agents) {
			state, err := LoadState(projectHash)
			if os.IsNotExist(err) {
				return fmt.Errorf("this host has no state for project %s, so the opeco QR port that `isotty up` allocated is unknown; run `isotty up` in the project directory, or attach with --no-host", projectHash)
			}
			if err != nil {
				return err
			}
			if err := addOpecoForward(forwardCfg, state.OpecoQRPort); err != nil {
				return err
			}
		}
	}
	return a.attachWithConnection(conn, projectHash, workspacePath, forwardCfg, epweaveCfg, githubCfg, googleCfg)
}

// fetchOptionalProjectFile returns nil when the VM's workspace has no such file.
func fetchOptionalProjectFile(conn vmcfg.GCPConnection, pathInWorkspace string) ([]byte, error) {
	data, err := vmcfg.FetchGCPProjectFile(conn, pathInWorkspace)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func (a *App) attachToState(projectPath string, state State, user string, noHost bool) error {
	forwardCfg := host.ForwardConfig{Forwards: map[string]host.Forward{}}
	var epweaveCfg *epweave.ProjectConfig
	var githubCfg *github.ProjectConfig
	var googleCfg *google.ProjectConfig
	var agents []string
	if !noHost {
		var err error
		forwardCfg, err = host.LoadForwardConfig(projectPath)
		if err != nil {
			return err
		}
		homeDir, err := isottyHome()
		if err != nil {
			return err
		}
		epweaveCfg, err = host.LoadEpweaveProjectConfig(projectPath, state.ProjectHash, homeDir)
		if err != nil {
			return err
		}
		githubCfg, err = host.LoadGitHubProjectConfig(projectPath, state.ProjectHash, homeDir)
		if err != nil {
			return err
		}
		googleCfg, err = host.LoadGoogleProjectConfig(projectPath, state.ProjectHash, homeDir)
		if err != nil {
			return err
		}
		agents, err = runtimecfg.ListAgents(projectPath)
		if err != nil {
			return err
		}
		if opeco.Active(agents) {
			if err := addOpecoForward(forwardCfg, state.OpecoQRPort); err != nil {
				return err
			}
		}
	}

	conn := vmcfg.GCPConnection{
		ProjectID:     state.GCPProjectID,
		Zone:          state.Zone,
		InstanceName:  state.InstanceName,
		User:          user,
		SSHConfigPath: state.SSHConfigPath,
	}
	workspacePath, err := vmcfg.GetGCPWorkspacePath(conn)
	if err != nil {
		return err
	}
	return a.attachWithConnection(conn, state.ProjectHash, workspacePath, forwardCfg, epweaveCfg, githubCfg, googleCfg)
}

const opecoForwardName = "opeco-qr"

// addOpecoForward exposes the VM's opeco QR page on the same host port while
// attached, so the URL opeco prints opens unchanged in the host browser. The
// port is the one `up` recorded in the project state.
func addOpecoForward(forwardCfg host.ForwardConfig, qrPort int) error {
	if qrPort == 0 {
		return errors.New("the project state has no opeco QR port; run `isotty up` to allocate one")
	}
	if _, exists := forwardCfg.Forwards[opecoForwardName]; exists {
		return fmt.Errorf("forward %q is reserved for the opeco QR page", opecoForwardName)
	}
	forwardCfg.Forwards[opecoForwardName] = host.Forward{LocalPort: qrPort, RemotePort: qrPort}
	return nil
}

// reverseForward exposes the host gateway on the VM's loopback for the attach session.
type reverseForward struct {
	VMPort   int
	HostPort int
}

func (a *App) attachWithConnection(conn vmcfg.GCPConnection, projectHash, workspacePath string, forwardCfg host.ForwardConfig, epweaveCfg *epweave.ProjectConfig, githubCfg *github.ProjectConfig, googleCfg *google.ProjectConfig) (err error) {
	names := host.SortedForwardNames(forwardCfg)
	target := a.auditTarget(conn)

	// Pad writes and browser-open requests are recorded in the VM event log
	// while the interactive session owns the terminal, so gcloud output must
	// never reach it (debug is forced off).
	record := func(record runtimecfg.VMEventRecord) error {
		return runtimecfg.RecordVMEvent(target, record, false)
	}
	// Open requests are answered before their event is recorded, so a failed
	// record has no request to fail; it fails the attach once the session ends.
	var deferredErrs []error
	var deferredMu sync.Mutex
	mux := http.NewServeMux()
	var services []string
	if epweaveCfg != nil {
		handler, err := a.epweaveHandler(*epweaveCfg, projectHash, record)
		if err != nil {
			return err
		}
		mux.Handle("/epweave/", http.StripPrefix("/epweave", handler))
		services = append(services, "ep_weave")
	}
	if githubCfg != nil {
		handler, err := a.githubHandler(*githubCfg, projectHash, record)
		if err != nil {
			return err
		}
		mux.Handle("/github/", http.StripPrefix("/github", handler))
		services = append(services, "GitHub")
	}
	if googleCfg != nil {
		handler, err := a.googleHandler(*googleCfg, projectHash, record)
		if err != nil {
			return err
		}
		mux.Handle("/google/", http.StripPrefix("/google", handler))
		services = append(services, "Google")
	}
	if qr, ok := forwardCfg.Forwards[opecoForwardName]; ok {
		mux.Handle("/open", opeco.OpenHandler(qr.RemotePort, opeco.OpenInBrowser, func(url string) {
			event, err := runtimecfg.NewOpenVMEvent(projectHash, url)
			if err == nil {
				err = record(event)
			}
			if err != nil {
				deferredMu.Lock()
				deferredErrs = append(deferredErrs, fmt.Errorf("record open-url event for %s: %w", url, err))
				deferredMu.Unlock()
			}
		}))
		services = append(services, "browser open")
	}
	var reverse *reverseForward
	if len(services) > 0 {
		gw, err := gateway.Serve(mux)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := gw.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}()
		reverse = &reverseForward{VMPort: gateway.VMPort, HostPort: gw.Port()}
	}
	sshArgs := buildAttachSSHArgs(conn, workspacePath, forwardCfg, reverse)

	var with []string
	if len(names) > 0 {
		with = append(with, fmt.Sprintf("%d forwards", len(names)))
	}
	if reverse != nil {
		with = append(with, fmt.Sprintf("host gateway (%s) on VM port %d", strings.Join(services, ", "), reverse.VMPort))
	}
	if len(with) > 0 {
		a.phase("Attaching to %s with %s", conn.InstanceName, strings.Join(with, " and "))
	} else {
		a.phase("Attaching to %s", conn.InstanceName)
	}

	startEvent, err := runtimecfg.NewAttachVMEvent(projectHash, "attach-start", len(names), "")
	if err != nil {
		return err
	}
	if err := runtimecfg.RecordVMEvent(target, startEvent, a.debug); err != nil {
		return fmt.Errorf("record attach-start event: %w", err)
	}

	if err := vmcfg.RefreshGCPSSHConfig(conn, a.debug); err != nil {
		return fmt.Errorf("refresh SSH config: %w", err)
	}

	attachErr := vmcfg.RunGCPInteractiveSSH(sshArgs...)

	result := "ok"
	if attachErr != nil {
		result = attachErr.Error()
	}
	endEvent, err := runtimecfg.NewAttachVMEvent(projectHash, "attach-end", len(names), result)
	if err != nil {
		if attachErr != nil {
			return fmt.Errorf("attach session failed: %v; resolve attach-end event context: %w", attachErr, err)
		}
		return err
	}
	if err := runtimecfg.RecordVMEvent(target, endEvent, a.debug); err != nil {
		if attachErr != nil {
			return fmt.Errorf("attach session failed: %v; record attach-end event: %w", attachErr, err)
		}
		return fmt.Errorf("record attach-end event: %w", err)
	}
	deferredMu.Lock()
	defer deferredMu.Unlock()
	return errors.Join(append([]error{attachErr}, deferredErrs...)...)
}

// epweaveHandler connects to ep_weave with the host credentials and serves
// the project's Notes, recording every pad write in the VM event log.
func (a *App) epweaveHandler(cfg epweave.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	hostCfg, err := epweave.LoadHostConfig(homeDir)
	if err != nil {
		return nil, err
	}
	notes, err := epweave.Connect(hostCfg, cfg, "isotty-"+projectHash, "isotty:"+cfg.Prefix)
	if err != nil {
		return nil, fmt.Errorf("connect to ep_weave: %w", err)
	}
	return epweave.Handler(notes, func(event epweave.WriteEvent) error {
		vmEvent, err := runtimecfg.NewEpweaveVMEvent(projectHash, event.Operation, event.Title, event.PadID)
		if err != nil {
			return err
		}
		return record(vmEvent)
	}), nil
}

// githubHandler relays the pinned repositories with the host token, recording
// every push and write in the VM event log.
func (a *App) githubHandler(cfg github.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	hostCfg, err := github.LoadHostConfig(homeDir)
	if err != nil {
		return nil, err
	}
	return github.Handler(hostCfg.Token, cfg, func(event github.WriteEvent) error {
		vmEvent, err := runtimecfg.NewGitHubVMEvent(projectHash, event.Operation, event.Repo, event.Target)
		if err != nil {
			return err
		}
		return record(vmEvent)
	}), nil
}

// googleHandler issues tokens for the pinned service account from the key on
// the host, recording every mint in the VM event log.
func (a *App) googleHandler(cfg google.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	key, err := google.LoadKey(homeDir, cfg.ServiceAccount)
	if err != nil {
		return nil, err
	}
	issuer, err := google.NewIssuer(key, cfg)
	if err != nil {
		return nil, err
	}
	return google.Handler(issuer, func(event google.MintEvent) error {
		vmEvent, err := runtimecfg.NewGoogleVMEvent(projectHash, event.ServiceAccount, event.Scopes)
		if err != nil {
			return err
		}
		return record(vmEvent)
	}), nil
}

func buildAttachSSHArgs(conn vmcfg.GCPConnection, workspacePath string, forwardCfg host.ForwardConfig, reverse *reverseForward) []string {
	instanceTarget := fmt.Sprintf("%s.%s.%s", conn.InstanceName, conn.Zone, conn.ProjectID)
	if conn.User != "" {
		instanceTarget = conn.User + "@" + instanceTarget
	}
	sshArgs := []string{
		"-F", conn.SSHConfigPath,
		"-tt",
		// A forward that cannot bind must fail the attach instead of leaving
		// a session whose forwards silently point nowhere.
		"-o", "ExitOnForwardFailure=yes",
		// Detect a dead peer within ~45s so a dropped session releases its
		// forwards instead of lingering until TCP gives up.
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
	}
	for _, name := range host.SortedForwardNames(forwardCfg) {
		forward := forwardCfg.Forwards[name]
		sshArgs = append(sshArgs, "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", forward.LocalPort, forward.RemotePort))
	}
	if reverse != nil {
		sshArgs = append(sshArgs, "-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", reverse.VMPort, reverse.HostPort))
	}
	sshArgs = append(sshArgs, instanceTarget, fmt.Sprintf("cd %s && exec ${SHELL:-/bin/bash} -l", workspacePath))
	return sshArgs
}

func (a *App) runDown(args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	force := fs.Bool("force", false, "remove the VM even when agent logins cannot be saved")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("down does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	state, err := LoadStateForProject(projectPath)
	if err != nil {
		return err
	}

	exists, err := vmcfg.GCPInstanceExists(state.GCPProjectID, state.Zone, state.InstanceName)
	if err != nil {
		return fmt.Errorf("check instance: %w", err)
	}

	// The login archive is written into the synced .isotty/, so sync stops
	// first; a VM about to be deleted has no use for it.
	if err := a.runPhase("Stopping sync session", func() error {
		return terminateMutagenSession(state)
	}); err != nil {
		return fmt.Errorf("terminate sync session: %w", err)
	}
	if exists {
		if err := a.saveAgentAuth(state); err != nil {
			if !*force {
				return fmt.Errorf("%w\nhint: `isotty down --force` removes the VM without saving; sync is stopped until the next `isotty up`", err)
			}
			fmt.Fprintf(a.stderr, "warning: agent logins were not saved: %v\n", err)
		}
	}

	if exists {
		if err := a.runPhase("Deleting VM %s", func() error {
			return vmcfg.DeleteGCPInstance(vmcfg.GCPConnection{
				ProjectID:    state.GCPProjectID,
				Zone:         state.Zone,
				InstanceName: state.InstanceName,
			}, a.debug)
		}, state.InstanceName); err != nil {
			return fmt.Errorf("delete instance: %w", err)
		}
	}

	if err := a.runPhase("Removing local state", func() error {
		return DeleteState(state.ProjectHash)
	}); err != nil {
		return fmt.Errorf("remove state: %w", err)
	}

	fmt.Fprintf(a.stdout, "IsoTTY environment removed.\n")
	return nil
}

func (a *App) runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("version does not accept arguments")
	}

	fmt.Fprintf(a.stdout, "isotty %s\n", Version())

	if err := a.printDependencyVersion("gcloud", []string{"version"}); err != nil {
		return err
	}
	if err := a.printDependencyVersion("mutagen", []string{"version"}); err != nil {
		return err
	}
	if err := a.printDependencyVersion("ssh", []string{"-V"}); err != nil {
		return err
	}
	return nil
}

func (a *App) runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("status does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	state, err := LoadStateForProject(projectPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "Project: %s\n", state.ProjectPath)
	fmt.Fprintf(a.stdout, "Instance: %s\n", state.InstanceName)
	fmt.Fprintf(a.stdout, "Project ID: %s\n", state.GCPProjectID)
	fmt.Fprintf(a.stdout, "Zone: %s\n", state.Zone)
	fmt.Fprintf(a.stdout, "Sync mode: %s\n", state.SyncMode)
	fmt.Fprintf(a.stdout, "Mutagen data dir: %s\n", state.MutagenDataDirectory)
	fmt.Fprintf(a.stdout, "Remote endpoint: %s\n", state.RemoteEndpoint())
	fmt.Fprintln(a.stdout)

	output, err := describeMutagenSession(state)
	if err != nil {
		return fmt.Errorf("query mutagen session: %w", err)
	}
	fmt.Fprint(a.stdout, output)
	return nil
}

func (a *App) runRuntime(args []string) error {
	if len(args) == 0 {
		return errors.New("runtime requires a subcommand: apt, service, post-install, node, agent, or tools")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	switch args[0] {
	case "apt":
		return runtimecfg.RunApt(projectPath, args[1:], a.stdout, a.stderr)
	case "service":
		return runtimecfg.RunService(projectPath, args[1:], a.stdout, a.stderr)
	case "post-install":
		return runtimecfg.RunPostInstall(projectPath, args[1:], a.stdout, a.stderr)
	case "node":
		return runtimecfg.RunNode(projectPath, args[1:], a.stdout, a.stderr)
	case "agent":
		if err := runtimecfg.RunAgent(projectPath, args[1:], a.stdout, a.stderr); err != nil {
			return err
		}
		if len(args) > 2 && args[1] == "remove" {
			return runtimecfg.RemoveAgentAuth(projectPath, args[2:])
		}
		return nil
	case "tools":
		return runtimecfg.RunTools(projectPath, args[1:], a.stdout, a.stderr)
	default:
		return fmt.Errorf("unknown runtime subcommand %q", args[0])
	}
}

func (a *App) runHost(args []string) error {
	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	return host.Run(projectPath, hashProjectPath(projectPath), homeDir, args, a.stdout, a.stderr)
}

func (a *App) runVM(args []string) error {
	if len(args) == 0 {
		return errors.New("vm requires a subcommand: gcp")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	switch args[0] {
	case "gcp":
		return vmcfg.RunGCP(projectPath, args[1:], a.stdout, a.stderr)
	default:
		return fmt.Errorf("unknown vm subcommand %q", args[0])
	}
}

func (a *App) printDependencyVersion(name string, args []string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		fmt.Fprintf(a.stdout, "%s: not found\n", name)
		return nil
	}

	fmt.Fprintf(a.stdout, "%s: %s\n", name, path)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"check %s version: command failed: %s %s: %w\nstdout:\n%s\nstderr:\n%s",
			name,
			name,
			strings.Join(args, " "),
			err,
			strings.TrimRight(stdout.String(), "\n"),
			strings.TrimRight(stderr.String(), "\n"),
		)
	}
	output := stdout.String()

	fmt.Fprintln(a.stdout, "")
	fmt.Fprint(a.stdout, output)
	if len(output) > 0 && output[len(output)-1] != '\n' {
		fmt.Fprintln(a.stdout, "")
	}
	return nil
}

func requireExecutable(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is required but was not found in PATH", name)
	}
	return nil
}

func (a *App) ensureEnvironment(cfg Config, syncMode string) (State, error) {
	state := NewState(cfg, syncMode)
	created := false
	gcpConfig := cfg.VM.GCP
	homeArchive, hasHomeArchive, err := runtimecfg.HomeFilesArchive(cfg.HomeDir)
	if err != nil {
		return State{}, err
	}
	previous, stateErr := LoadStateForProject(cfg.ProjectPath)
	hasState := stateErr == nil
	if stateErr != nil && !IsStateNotFoundError(stateErr) {
		return State{}, stateErr
	}
	if hasState {
		state.OpecoQRPort = previous.OpecoQRPort
	}
	if opeco.Active(cfg.Runtime.Agents) && state.OpecoQRPort == 0 {
		others, err := ListStates()
		if err != nil {
			return State{}, err
		}
		var taken []int
		for _, other := range others {
			if other.ProjectHash != state.ProjectHash && other.OpecoQRPort != 0 {
				taken = append(taken, other.OpecoQRPort)
			}
		}
		state.OpecoQRPort = opeco.ChooseQRPort(taken)
	}
	exists, err := vmcfg.GCPInstanceExists(cfg.GCPProjectID, cfg.Zone, state.InstanceName)
	if err != nil {
		return State{}, fmt.Errorf("check instance: %w", err)
	}
	if !hasState && exists {
		return State{}, fmt.Errorf("instance %s already exists but no local IsoTTY state was found; use `isotty attach --target <id>` or remove the VM explicitly", state.InstanceName)
	}
	if !exists {
		if err := a.runPhase("Creating VM %s", func() error {
			return vmcfg.CreateGCPInstance(vmcfg.GCPInstanceSpec{
				ProjectID:    cfg.GCPProjectID,
				Zone:         cfg.Zone,
				ProjectHash:  cfg.ProjectHash,
				MachineType:  *gcpConfig.MachineType,
				BootDiskSize: *gcpConfig.BootDiskSize,
				ImageFamily:  *gcpConfig.ImageFamily,
				ImageProject: *gcpConfig.ImageProject,
			}, state.InstanceName, a.debug)
		}, state.InstanceName); err != nil {
			return State{}, err
		}
		created = true
	} else {
		a.phase("Reusing VM %s", state.InstanceName)
	}

	if err := a.runPhase("Saving local state", func() error {
		return SaveState(state)
	}); err != nil {
		return State{}, fmt.Errorf("save state: %w", err)
	}

	if err := a.runPhase("Waiting for SSH", func() error {
		return vmcfg.WaitForGCPSSH(vmcfg.GCPConnection{
			ProjectID:    state.GCPProjectID,
			Zone:         state.Zone,
			InstanceName: state.InstanceName,
		})
	}); err != nil {
		return State{}, err
	}
	workspacePath, err := vmcfg.GetGCPWorkspacePath(vmcfg.GCPConnection{
		ProjectID:    state.GCPProjectID,
		Zone:         state.Zone,
		InstanceName: state.InstanceName,
	})
	if err != nil {
		return State{}, err
	}
	state.RemoteWorkspacePath = workspacePath
	if created {
		command, err := runtimecfg.BootstrapCommand(cfg.Runtime, state.RemoteWorkspacePath)
		if err != nil {
			return State{}, err
		}
		if err := a.runPhase(runtimecfg.BootstrapLabel(cfg.Runtime), func() error {
			return vmcfg.RunGCPRemoteCommand(vmcfg.GCPConnection{
				ProjectID:    state.GCPProjectID,
				Zone:         state.Zone,
				InstanceName: state.InstanceName,
			}, command, a.debug)
		}); err != nil {
			return State{}, err
		}
		if hasHomeArchive {
			if err := a.runPhase("Copying %s into the VM home", func() error {
				return vmcfg.RunGCPRemoteCommandWithInput(vmcfg.GCPConnection{
					ProjectID:    state.GCPProjectID,
					Zone:         state.Zone,
					InstanceName: state.InstanceName,
				}, runtimecfg.UnpackHomeCommand(), bytes.NewReader(homeArchive))
			}, runtimecfg.HomeFilesDir(cfg.HomeDir)); err != nil {
				return State{}, err
			}
		}
		if err := a.restoreAgentAuth(cfg, state); err != nil {
			return State{}, err
		}
	} else {
		a.phase("Skipping bootstrap for existing VM %s", state.InstanceName)
	}
	if err := a.runPhase("Refreshing SSH config", func() error {
		return vmcfg.RefreshGCPSSHConfig(vmcfg.GCPConnection{
			ProjectID:     state.GCPProjectID,
			Zone:          state.Zone,
			InstanceName:  state.InstanceName,
			SSHConfigPath: state.SSHConfigPath,
		}, a.debug)
	}); err != nil {
		return State{}, err
	}
	if err := a.runPhase("Preparing SSH wrappers", func() error {
		return ensureSSHWrappers(state)
	}); err != nil {
		return State{}, err
	}
	if err := a.runPhase("Configuring audit", func() error {
		return runtimecfg.ConfigureAudit(a.auditTarget(vmcfg.GCPConnection{
			ProjectID:    state.GCPProjectID,
			Zone:         state.Zone,
			InstanceName: state.InstanceName,
		}), a.debug)
	}); err != nil {
		return State{}, err
	}
	if cfg.Epweave != nil {
		if err := a.runPhase("Installing ep_weave client", func() error {
			return vmcfg.RunGCPRemoteCommand(vmcfg.GCPConnection{
				ProjectID:    state.GCPProjectID,
				Zone:         state.Zone,
				InstanceName: state.InstanceName,
			}, epweave.SetupCommand(*cfg.Epweave, cfg.Runtime.Agents), a.debug)
		}); err != nil {
			return State{}, err
		}
	}
	if cfg.GitHub != nil {
		if err := a.runPhase("Installing GitHub client", func() error {
			return vmcfg.RunGCPRemoteCommand(vmcfg.GCPConnection{
				ProjectID:    state.GCPProjectID,
				Zone:         state.Zone,
				InstanceName: state.InstanceName,
			}, github.SetupCommand(cfg.Runtime.Agents), a.debug)
		}); err != nil {
			return State{}, err
		}
	}
	if cfg.Google != nil {
		if err := a.runPhase("Installing Google client", func() error {
			return vmcfg.RunGCPRemoteCommand(vmcfg.GCPConnection{
				ProjectID:    state.GCPProjectID,
				Zone:         state.Zone,
				InstanceName: state.InstanceName,
			}, google.SetupCommand(cfg.Runtime.Agents), a.debug)
		}); err != nil {
			return State{}, err
		}
	}
	if opeco.Active(cfg.Runtime.Agents) {
		if err := a.runPhase("Installing opeco (QR page on port %d)", func() error {
			return vmcfg.RunGCPRemoteCommand(vmcfg.GCPConnection{
				ProjectID:    state.GCPProjectID,
				Zone:         state.Zone,
				InstanceName: state.InstanceName,
			}, opeco.SetupCommand(cfg.Runtime.Agents, state.OpecoQRPort), a.debug)
		}, state.OpecoQRPort); err != nil {
			return State{}, err
		}
	}
	if err := a.runPhase("Starting sync session", func() error {
		return recreateMutagenSession(state, a.debug)
	}); err != nil {
		return State{}, err
	}
	if created {
		hasPostInstall, err := runtimecfg.HasPostInstallScript(state.ProjectPath)
		if err != nil {
			return State{}, err
		}
		if hasPostInstall {
			if err := a.runPhase("Running post-install script", func() error {
				return vmcfg.RunGCPRemoteCommand(vmcfg.GCPConnection{
					ProjectID:    state.GCPProjectID,
					Zone:         state.Zone,
					InstanceName: state.InstanceName,
				}, runtimecfg.PostInstallCommand(state.RemoteWorkspacePath), a.debug)
			}); err != nil {
				return State{}, err
			}
		}
	}
	state.BootstrapCompleted = true
	if err := a.runPhase("Saving local state", func() error {
		return SaveState(state)
	}); err != nil {
		return State{}, fmt.Errorf("save state: %w", err)
	}

	return state, nil
}

// saveAgentAuth stores each configured agent's home state from the VM so the
// next VM of this project starts logged in.
func (a *App) saveAgentAuth(state State) error {
	agents, err := runtimecfg.ListAgents(state.ProjectPath)
	if err != nil {
		return err
	}
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	conn := vmcfg.GCPConnection{ProjectID: state.GCPProjectID, Zone: state.Zone, InstanceName: state.InstanceName}
	for _, agent := range agents {
		if err := a.runPhase("Saving %s login state", func() error {
			output, err := vmcfg.CaptureGCPRemoteCommand(conn, runtimecfg.CaptureAgentAuthCommand(agent))
			if err != nil {
				return err
			}
			archive, err := base64.StdEncoding.DecodeString(strings.TrimSpace(output))
			if err != nil {
				return fmt.Errorf("decode %s login state: %w", agent, err)
			}
			return runtimecfg.SaveAgentAuth(homeDir, state.ProjectPath, agent, archive)
		}, agent); err != nil {
			return fmt.Errorf("save %s login state: %w", agent, err)
		}
	}
	return nil
}

func (a *App) restoreAgentAuth(cfg Config, state State) error {
	conn := vmcfg.GCPConnection{ProjectID: state.GCPProjectID, Zone: state.Zone, InstanceName: state.InstanceName}
	for _, agent := range cfg.Runtime.Agents {
		archive, ok, err := runtimecfg.LoadAgentAuth(cfg.HomeDir, cfg.ProjectPath, agent)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := a.runPhase("Restoring %s login state", func() error {
			return vmcfg.RunGCPRemoteCommandWithInput(conn, runtimecfg.UnpackAgentAuthCommand(agent), bytes.NewReader(archive))
		}, agent); err != nil {
			return fmt.Errorf("restore %s login state: %w", agent, err)
		}
	}
	return nil
}

func (a *App) phase(format string, args ...any) {
	if a.debug {
		return
	}
	fmt.Fprintf(a.stdout, "==> "+format+"\n", args...)
}

func (a *App) runPhase(format string, fn func() error, args ...any) error {
	if a.debug {
		return fn()
	}
	label := fmt.Sprintf(format, args...)
	if !a.isTTY() {
		a.phase("%s", label)
		return fn()
	}
	s := newSpinner(a.stdout, label)
	s.start()
	err := fn()
	if err != nil {
		s.stopFailure()
		return err
	}
	s.stopSuccess()
	return nil
}

func (a *App) followAuditLogs(state State) error {
	seen := map[string]struct{}{}
	for {
		events, err := runtimecfg.QueryAuditLogs(a.auditTarget(vmcfg.GCPConnection{
			ProjectID:    state.GCPProjectID,
			Zone:         state.Zone,
			InstanceName: state.InstanceName,
		}), "recent")
		if err != nil {
			return err
		}
		for _, event := range events {
			if _, ok := seen[event.EventID]; ok {
				continue
			}
			seen[event.EventID] = struct{}{}
			a.printAuditEvent(event)
		}
		time.Sleep(2 * time.Second)
	}
}

func (a *App) printAuditEvents(events []runtimecfg.AuditEvent) {
	for _, event := range events {
		a.printAuditEvent(event)
	}
}

func (a *App) printAuditEvent(event runtimecfg.AuditEvent) {
	timestamp := event.Time.Format(time.RFC3339)
	switch event.Kind {
	case "exec":
		fmt.Fprintf(a.stdout, "%s exec %s\n", timestamp, terminalSafe(event.Command))
	case "connect":
		if event.Address != "" {
			fmt.Fprintf(a.stdout, "%s connect %s\n", timestamp, terminalSafe(event.Address))
			return
		}
		fmt.Fprintf(a.stdout, "%s connect %s\n", timestamp, terminalSafe(event.Executable))
	default:
		fmt.Fprintf(a.stdout, "%s %s\n", timestamp, terminalSafe(event.RawSummary()))
	}
}

// terminalSafe escapes control characters so a value the VM chose cannot
// drive the human's terminal.
func terminalSafe(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || (r >= ' ' && r != 0x7f) {
			return r
		}
		return '\uFFFD'
	}, value)
}

func (a *App) auditTarget(conn vmcfg.GCPConnection) runtimecfg.RemoteTarget {
	return runtimecfg.RemoteTarget{
		Run: func(command string, debug bool) error {
			return vmcfg.RunGCPRemoteCommand(conn, command, debug)
		},
		Capture: func(command string) (string, error) {
			return vmcfg.CaptureGCPRemoteCommand(conn, command)
		},
		ExitCode: vmcfg.GCPCommandExitCode,
	}
}

func validateSyncMode(mode string) error {
	if _, ok := supportedSyncModes[mode]; ok {
		return nil
	}
	return fmt.Errorf("unsupported sync mode %q", mode)
}
