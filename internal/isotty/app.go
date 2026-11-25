package isotty

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host"
	"github.com/yazawa/isotty/internal/isotty/host/aws"
	"github.com/yazawa/isotty/internal/isotty/host/bitbucket"
	"github.com/yazawa/isotty/internal/isotty/host/browser"
	"github.com/yazawa/isotty/internal/isotty/host/child"
	"github.com/yazawa/isotty/internal/isotty/host/dns"
	"github.com/yazawa/isotty/internal/isotty/host/docker"
	"github.com/yazawa/isotty/internal/isotty/host/epweave"
	"github.com/yazawa/isotty/internal/isotty/host/forge"
	"github.com/yazawa/isotty/internal/isotty/host/gas"
	"github.com/yazawa/isotty/internal/isotty/host/github"
	"github.com/yazawa/isotty/internal/isotty/host/gitlab"
	"github.com/yazawa/isotty/internal/isotty/host/google"
	"github.com/yazawa/isotty/internal/isotty/host/redmine"
	"github.com/yazawa/isotty/internal/isotty/host/sshhost"
	"github.com/yazawa/isotty/internal/isotty/host/wake"
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
	case "stop":
		return a.runStop(args[1:])
	case "schedule":
		return a.runSchedule(args[1:])
	case "daemon":
		return a.runDaemon(args[1:])
	case "status":
		return a.runStatus(args[1:])
	case "list":
		return a.runList(args[1:])
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
	fmt.Fprintln(a.stdout, "  isotty [--debug] attach [--user <name>] [--no-tmux]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] down [--force]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] stop")
	fmt.Fprintln(a.stdout, "  isotty [--debug] schedule enable|disable|show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] daemon status|restart|stop")
	fmt.Fprintln(a.stdout, "  isotty [--debug] audit logs [-f] [--archive <file>]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] audit archive")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host forward add <name> --local-port <port> --remote-port <port>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host forward list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host forward remove <name>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host epweave enable --prefix <name>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host epweave disable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host epweave show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host github add [--remote <name>]... [--repo <owner/name>]... [--allow read|comment|write|admin]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host github remove [--remote <name>]... [--repo <owner/name>]...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host github clear")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host github show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gitlab login --url <https://instance>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gitlab add [--remote <name>]... [--repo <host/group/project>]... [--allow read|comment|write|admin]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gitlab remove [--remote <name>]... [--repo <host/group/project>]...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gitlab clear")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gitlab show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host bitbucket login")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host bitbucket add [--remote <name>]... [--repo <workspace/repo_slug>]... [--allow read|comment|write|admin]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host bitbucket remove [--remote <name>]... [--repo <workspace/repo_slug>]...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host bitbucket clear")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host bitbucket show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host redmine login <url>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host redmine add --project <identifier>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host redmine remove --project <identifier>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host redmine clear")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host redmine show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host docker add --repo <registry/path>... [--allow read|write]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host docker remove --repo <registry/path>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host docker clear")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host docker show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host google enable [--key <service-account.json> | --impersonate <service-account>] --scope <scope>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host google disable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host google show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host aws enable --role-arn <arn> [--profile <name>]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host aws disable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host aws show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gas add --script <id>... --client <client_secret.json> --scope <url>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gas remove --script <id>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gas clear")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host gas show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host dns add --project <id> --name <record name>... [--managed-zone <name>]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host dns remove --name <record name>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host dns show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host children enable [--machine-type <type>]... [--max <n>] [--boot-disk-size <size>] [--expose <port>]...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host children disable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host children show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host ssh add <name> --host <host> --user <user> [--port <port>] [--key-type ed25519|ecdsa]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host ssh remove <name> [--force]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host ssh show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host opeco enable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host opeco disable")
	fmt.Fprintln(a.stdout, "  isotty [--debug] host opeco show")
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
	fmt.Fprintln(a.stdout, "  isotty [--debug] list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] version")
}

func (a *App) runUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	syncMode := fs.String("sync", defaultSyncMode, "synchronization mode")
	user := fs.String("user", "", "SSH username to use for auto-attach")
	noAttach := fs.Bool("no-attach", false, "do not attach after preparing the environment")
	ignoreRunning := fs.Bool("ignore-running", false, "go on without waiting for what an earlier, interrupted up left running in the VM; its steps may then not reach their result")
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

	state, err := a.ensureEnvironment(cfg, *syncMode, *ignoreRunning)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "IsoTTY environment is ready.\n")
	fmt.Fprintf(a.stdout, "Instance: %s\n", state.InstanceName)
	fmt.Fprintf(a.stdout, "Project: %s\n", state.GCPProjectID)
	fmt.Fprintf(a.stdout, "Zone: %s\n", state.Zone)
	fmt.Fprintf(a.stdout, "Sync mode: %s\n", state.SyncMode)
	// The daemon holds the forwards and the gateway while the VM runs; it
	// reads the project again here, which is where a change to the host's
	// agreements takes effect.
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	if err := a.runPhase("Handing the project to the isotty daemon", func() error {
		if err := a.ensureDaemon(homeDir); err != nil {
			return err
		}
		return reloadProject(homeDir, state.ProjectHash)
	}); err != nil {
		return err
	}
	if *noAttach {
		return nil
	}
	return a.attachToState(state, *user, false)
}

func (a *App) runAudit(args []string) error {
	if len(args) == 0 {
		return errors.New("audit requires a subcommand: logs")
	}

	switch args[0] {
	case "logs":
		return a.runAuditLogs(args[1:])
	case "archive":
		return a.runAuditArchive(args[1:])
	default:
		return fmt.Errorf("unknown audit subcommand %q", args[0])
	}
}

// runAuditArchive keeps the VM's audit trail with the project, sealed; it
// is what `down` does before deleting the VM, and can be run at any time.
func (a *App) runAuditArchive(args []string) error {
	fs := flag.NewFlagSet("audit archive", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("audit archive does not accept arguments")
	}
	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	state, err := LoadStateForProject(projectPath)
	if err != nil {
		return err
	}
	path, err := a.archiveAudit(state)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Audit archive written to %s\n", path)
	return nil
}

func (a *App) archiveAudit(state State) (string, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return "", err
	}
	conn := vmcfg.GCPConnection{ProjectID: state.GCPProjectID, Zone: state.Zone, InstanceName: state.InstanceName}
	stamp := runtimecfg.AuditArchiveStamp(time.Now())
	var path string
	err = a.runPhase("Archiving audit logs", func() error {
		// The VM streams the tar.gz; it is sealed as it arrives.
		pr, pw := io.Pipe()
		captured := make(chan error, 1)
		go func() {
			err := a.auditTarget(conn).CaptureTo(runtimecfg.CaptureAuditArchiveCommand(stamp), pw)
			pw.CloseWithError(err)
			captured <- err
		}()
		saved, err := runtimecfg.SaveAuditArchive(homeDir, state.ProjectPath, stamp, base64.NewDecoder(base64.StdEncoding, pr))
		pr.Close()
		if captureErr := <-captured; captureErr != nil {
			return captureErr
		}
		if err != nil {
			return err
		}
		path = saved
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("archive audit logs: %w", err)
	}
	return path, nil
}

func (a *App) runAuditLogs(args []string) error {
	fs := flag.NewFlagSet("audit logs", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	follow := fs.Bool("f", false, "follow logs")
	archive := fs.String("archive", "", "read an archive written by `isotty audit archive` or `isotty down` instead of the VM")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("audit logs does not accept positional arguments")
	}
	if *archive != "" {
		if *follow {
			return errors.New("audit logs -f cannot follow an archive")
		}
		return a.printAuditArchive(*archive)
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

func (a *App) printAuditArchive(path string) error {
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	stream, err := runtimecfg.OpenAuditArchive(homeDir, path)
	if err != nil {
		return err
	}
	defer stream.Close()
	events, err := runtimecfg.ReadAuditArchive(stream)
	if err != nil {
		return err
	}
	a.printAuditEvents(events)
	return nil
}

func (a *App) runAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	user := fs.String("user", "", "SSH username to use for attach")
	noTmux := fs.Bool("no-tmux", false, "open a plain login shell instead of the tmux session")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("attach does not accept arguments")
	}
	if err := requireExecutable("ssh"); err != nil {
		return err
	}
	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	state, err := LoadStateForProject(projectPath)
	if err != nil {
		return err
	}
	if err := requireRunningInstance(state.GCPProjectID, state.Zone, state.InstanceName); err != nil {
		return err
	}
	return a.attachToState(state, *user, *noTmux)
}

// requireRunningInstance refuses to attach to a VM that is stopped or in
// transition, where ssh would only time out; `up` is what starts it.
func requireRunningInstance(projectID, zone, instanceName string) error {
	status, err := vmcfg.GCPInstanceStatus(projectID, zone, instanceName)
	if err != nil {
		return fmt.Errorf("check instance: %w", err)
	}
	if status == "" {
		return fmt.Errorf("instance %s was not found", instanceName)
	}
	if status != vmcfg.GCPStatusRunning {
		return fmt.Errorf("instance %s is %s; `isotty up` in the project directory starts it", instanceName, status)
	}
	return nil
}

// attachToState opens a terminal into the VM. The forwards and the gateway
// are the daemon's, which it holds while the VM runs; the attach makes
// sure it runs and says how it holds the project.
func (a *App) attachToState(state State, user string, noTmux bool) error {
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	shell, err := attachShellFor(homeDir, noTmux)
	if err != nil {
		return err
	}
	a.describeHold(homeDir, state)
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
	return a.attachTerminal(conn, state.ProjectHash, workspacePath, shell)
}

// describeHold says in one line how the daemon holds the project, so a
// human sees a missing gateway before an agent trips on it. A daemon that
// cannot be started leaves the terminal usable.
func (a *App) describeHold(homeDir string, state State) {
	if err := a.ensureDaemon(homeDir); err != nil {
		fmt.Fprintf(a.stderr, "warning: the isotty daemon is not running, so the VM has no forwards and no gateway: %v\n", err)
		return
	}
	statuses, err := daemonStatus(homeDir)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: %v\n", err)
		return
	}
	for _, s := range statuses {
		if s.ProjectHash != state.ProjectHash {
			continue
		}
		line := "Host daemon: gateway " + s.Gateway
		if len(s.Services) > 0 {
			line += " (" + strings.Join(s.Services, ", ") + ")"
		}
		if n := len(s.Refused); n > 0 {
			line += fmt.Sprintf("; %d not served, see `isotty daemon status`", n)
		}
		a.phase("%s", line)
		return
	}
	if err := reloadProject(homeDir, state.ProjectHash); err != nil {
		fmt.Fprintf(a.stderr, "warning: the isotty daemon does not hold this project: %v\n", err)
		return
	}
	a.phase("Host daemon: taking the project up now")
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

// attachTerminal runs the terminal's ssh into the VM's tmux, attaching
// again after a drop, and records the session's start and end.
func (a *App) attachTerminal(conn vmcfg.GCPConnection, projectHash, workspacePath string, shell attachShell) error {
	target := a.auditTarget(conn)
	sshArgs := buildAttachSSHArgs(conn, workspacePath, shell)
	a.phase("Attaching to %s", conn.InstanceName)
	startEvent, err := runtimecfg.NewAttachVMEvent(projectHash, "attach-start", 0, "")
	if err != nil {
		return err
	}
	startCommand, err := runtimecfg.VMEventCommand(startEvent)
	if err != nil {
		return err
	}
	if err := a.waitAtTidepool(func() error {
		if err := target.Run(startCommand, a.debug); err != nil {
			return fmt.Errorf("record attach-start event: %w", err)
		}
		if err := vmcfg.RefreshGCPSSHConfig(conn, a.debug); err != nil {
			return fmt.Errorf("refresh SSH config: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	a.bell()
	attachErr := a.runSession(interactiveSSH, sshArgs, func() (string, error) {
		return vmcfg.GCPInstanceStatus(conn.ProjectID, conn.Zone, conn.InstanceName)
	})
	if errors.Is(attachErr, errVMStopped) {
		// The VM is off: there is no log to record the end in, and the
		// power-off is already there as idle-stop.
		a.phase("%v", errVMStopped)
		return nil
	}
	result := "ok"
	if attachErr != nil {
		result = attachErr.Error()
	}
	endEvent, err := runtimecfg.NewAttachVMEvent(projectHash, "attach-end", 0, result)
	if err != nil {
		return errors.Join(attachErr, err)
	}
	if err := runtimecfg.RecordVMEvent(target, endEvent, a.debug); err != nil {
		return errors.Join(attachErr, fmt.Errorf("record attach-end event: %w", err))
	}
	return attachErr
}

// A session that drops after reconnectAfter is attached again, once after
// each of reconnectDelays until one attaches, so a network that is gone for
// minutes, a laptop moving between networks say, is waited out. The delays
// grow so that a short drop comes back at once and a long one is still
// waited for, some six minutes of waiting in all. A session that ends
// sooner is not attached again: ssh ends with 255 for its own errors too,
// an authentication or a forward that cannot bind among them, and those end
// it right at the start. The first attach is that case and is not retried.
const reconnectAfter = 3 * time.Minute

var reconnectDelays = []time.Duration{5 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 3 * time.Minute}

// Replaced in tests.
var (
	interactiveSSH = vmcfg.RunGCPInteractiveSSH
	sessionSleep   = time.Sleep
	sessionNow     = time.Now
)

// errVMStopped ends a session whose VM powered itself off: no agent had
// work for the time the host set (see docs/VM.md, Idle Stop). Attaching
// again would only fail until the next `isotty up`.
var errVMStopped = errors.New("the VM powered itself off once no agent had work; `isotty up` starts it again")

// runSession runs the interactive ssh and attaches again whenever the
// connection drops after the session has run for a while; each such drop
// gets all of reconnectDelays anew. A drop because the VM stopped is not
// attached again.
func (a *App) runSession(run func(...string) error, sshArgs []string, vmStatus func() (string, error)) error {
	started := sessionNow()
	err := a.sshSession(run, sshArgs)
	for droppedAfter(err, sessionNow().Sub(started), reconnectAfter) {
		var attached bool
		started, attached, err = a.reconnect(run, sshArgs, vmStatus)
		if !attached {
			return err
		}
	}
	return err
}

// reconnect attaches again after a drop, and returns the session it got:
// its start and how it ended, or that none attached. Each attempt first asks
// GCP whether the VM still runs, and stops at errVMStopped when it does not.
// An attempt that fails otherwise, whatever the reason, is followed by the
// next one; the bell rings when the last one fails too.
func (a *App) reconnect(run func(...string) error, sshArgs []string, vmStatus func() (string, error)) (time.Time, bool, error) {
	var last error
	for n, delay := range reconnectDelays {
		status, err := vmStatus()
		if n == 0 && err == nil && status == vmcfg.GCPStatusRunning {
			// A guest power-off cuts ssh before GCP stops reporting the VM
			// as running.
			a.phase("The connection dropped; checking in %d seconds whether the VM is stopping", int(shutdownGrace.Seconds()))
			sessionSleep(shutdownGrace)
			status, err = vmStatus()
		}
		if err == nil && status != vmcfg.GCPStatusRunning {
			return time.Time{}, false, errVMStopped
		}
		if err != nil {
			// Asking GCP needs the network the drop may have taken.
			a.phase("Could not tell whether the VM still runs: %v", err)
		}
		a.phase("The connection dropped; attaching again in %d seconds (%d/%d)", int(delay.Seconds()), n+1, len(reconnectDelays))
		sessionSleep(delay)
		started := sessionNow()
		err = a.sshSession(run, sshArgs)
		if sshFailed(err) && sessionNow().Sub(started) < reconnectAfter {
			a.phase("Attaching again failed: %v", err)
			last = err
			continue
		}
		return started, true, err
	}
	a.bell()
	return time.Time{}, false, fmt.Errorf("the connection dropped and attaching again failed %d times; the last: %w", len(reconnectDelays), last)
}

// shutdownGrace is how long a drop waits before taking a VM GCP still
// reports as running for one that runs.
const shutdownGrace = 15 * time.Second

// sshSession runs the interactive ssh. One that ssh ended itself, a lost
// connection among them, leaves the terminal in the modes the session set:
// tmux turns on mouse reporting, focus events, bracketed paste and the
// alternate screen, and turns them off only when it detaches. A terminal
// left reporting the mouse types escape sequences into whatever reads it
// next, the attach's messages and the shell after it, so they are turned
// off here.
func (a *App) sshSession(run func(...string) error, sshArgs []string) error {
	err := run(sshArgs...)
	if sshFailed(err) && a.isTTY() {
		fmt.Fprint(a.stdout, terminalReset)
	}
	return err
}

// terminalReset turns off mouse reporting in each of its encodings, focus
// events and bracketed paste, leaves the alternate screen and shows the
// cursor.
const terminalReset = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1005l\x1b[?1006l\x1b[?1015l" +
	"\x1b[?1004l\x1b[?2004l\x1b[?1049l\x1b[?25h"

// sshFailed tells ssh ended with 255, its own failure, a lost connection
// among them, rather than with the status of the session's shell.
func sshFailed(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 255
}

// droppedAfter tells a session ssh ended with 255, its exit status for a
// lost connection, after running at least that long.
func droppedAfter(err error, ran, least time.Duration) bool {
	return sshFailed(err) && ran >= least
}

// bell rings the terminal the human attached from, where it shows as the
// tab's badge, a bounce or a sound.
func (a *App) bell() {
	if a.isTTY() {
		fmt.Fprint(a.stdout, "\a")
	}
}

// gatewayHandler mounts the host services the VM may use, which the daemon
// serves while the VM runs.
func (a *App) gatewayHandler(conn vmcfg.GCPConnection, projectHash string, files *browser.Files, epweaveCfg *epweave.ProjectConfig, githubCfg *github.ProjectConfig, gitlabCfg *gitlab.ProjectConfig, bitbucketCfg *bitbucket.ProjectConfig, redmineCfg *redmine.ProjectConfig, dockerCfg *docker.ProjectConfig, googleCfg *google.ProjectConfig, awsCfg *aws.ProjectConfig, gasCfg *gas.ProjectConfig, dnsCfg *dns.ProjectConfig, childCfg *child.ProjectConfig, sshRelay *sshhost.Relay, record func(runtimecfg.VMEventRecord) error, fail func(error)) (http.Handler, []string, error) {
	mux := http.NewServeMux()
	var services []string
	if epweaveCfg != nil {
		handler, err := a.epweaveHandler(*epweaveCfg, projectHash, record)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/epweave/", http.StripPrefix("/epweave", handler))
		services = append(services, "ep_weave")
	}
	if githubCfg != nil {
		handler, err := a.githubHandler(*githubCfg, projectHash, record)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/github/", http.StripPrefix("/github", handler))
		services = append(services, "GitHub")
	}
	if gitlabCfg != nil {
		handler, err := a.gitlabHandler(*gitlabCfg, projectHash, record)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/gitlab/", http.StripPrefix("/gitlab", handler))
		services = append(services, "GitLab")
	}
	if bitbucketCfg != nil {
		handler, err := a.bitbucketHandler(*bitbucketCfg, projectHash, record)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/bitbucket/", http.StripPrefix("/bitbucket", handler))
		services = append(services, "Bitbucket")
	}
	if redmineCfg != nil {
		handler, err := a.redmineHandler(*redmineCfg, conn, projectHash)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/redmine/", http.StripPrefix("/redmine", handler))
		services = append(services, "Redmine")
	}
	if dockerCfg != nil {
		handler, err := a.dockerHandler(*dockerCfg, projectHash, record, fail)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/docker/", http.StripPrefix("/docker", handler))
		services = append(services, "Docker")
	}
	if googleCfg != nil {
		handler, err := a.googleHandler(*googleCfg, projectHash, record)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/google/", http.StripPrefix("/google", handler))
		services = append(services, "Google")
	}
	if awsCfg != nil {
		handler, err := a.awsHandler(*awsCfg, projectHash, record)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/aws/", http.StripPrefix("/aws", handler))
		services = append(services, "AWS")
	}
	if gasCfg != nil {
		handler, err := a.gasHandler(*gasCfg, projectHash, record)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/gas/", http.StripPrefix("/gas", handler))
		services = append(services, "Apps Script")
	}
	if dnsCfg != nil {
		handler, err := a.dnsHandler(*dnsCfg, projectHash, record)
		if err != nil {
			return nil, nil, err
		}
		mux.Handle("/dns/", http.StripPrefix("/dns", handler))
		services = append(services, "DNS")
	}
	if childCfg != nil {
		mux.Handle("/child/", http.StripPrefix("/child", a.childHandler(*childCfg, conn, projectHash, record)))
		services = append(services, "child VMs")
	}
	if sshRelay != nil {
		mux.Handle("/ssh/", http.StripPrefix("/ssh", sshhost.Handler(sshRelay, func(event sshhost.Event) {
			vmEvent, err := runtimecfg.NewSSHVMEvent(projectHash, event.Operation, event.Name, event.Address, event.Sent, event.Received, event.Seconds, event.Result)
			if err == nil {
				err = record(vmEvent)
			}
			if err != nil {
				fail(fmt.Errorf("record ssh-%s event for %s: %w", event.Operation, event.Name, err))
			}
		})))
		services = append(services, "SSH hosts")
	}
	mux.Handle("/open", browser.Handler(browser.OpenInBrowser, func(target string) {
		event, err := runtimecfg.NewOpenVMEvent(projectHash, target)
		if err == nil {
			err = record(event)
		}
		if err != nil {
			fail(fmt.Errorf("record open-url event for %s: %w", target, err))
		}
	}, files))
	services = append(services, "browser open")
	homeDir, err := isottyHome()
	if err != nil {
		return nil, nil, err
	}
	mux.Handle("/wake/", http.StripPrefix("/wake", wake.Handler(homeDir, projectHash, func(kind string, request wake.Request) error {
		event, err := runtimecfg.NewWakeVMEvent(projectHash, kind, request.Agent, request.Session, request.At, request.Prompt, "")
		if err != nil {
			return err
		}
		return record(event)
	})))
	services = append(services, "wake")
	return mux, services, nil
}

// childHandler creates and deletes child VMs of the attached VM with the
// host's gcloud login, recording every one in the VM event log.
func (a *App) childHandler(cfg child.ProjectConfig, conn vmcfg.GCPConnection, projectHash string, record func(runtimecfg.VMEventRecord) error) http.Handler {
	defaults := child.Defaults{BootDiskSize: vmcfg.DefaultChildDiskSize, ImageFamily: vmcfg.DefaultChildImageFamily, ImageProject: vmcfg.DefaultChildImageProject}
	relay := child.NewRelay(cfg, child.GCP, conn, projectHash, defaults)
	return child.Handler(relay, func(event child.Event) error {
		vmEvent, err := runtimecfg.NewChildVMEvent(projectHash, event.Operation, event.Name, event.Instance, event.Shape, event.Expose, event.ExternalIP, event.Result)
		if err != nil {
			return err
		}
		return record(vmEvent)
	})
}

// dnsHandler changes records with the host's gcloud login within the
// pinned names, recording every change and refusal in the VM event log.
func (a *App) dnsHandler(cfg dns.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	relay := dns.NewRelay(cfg, dns.NewClient(), homeDir, projectHash)
	return dns.Handler(relay, func(event dns.ChangeEvent) error {
		vmEvent, err := runtimecfg.NewDNSVMEvent(projectHash, event.Operation, event.Zone, event.Name, event.Type, event.Before, event.After, event.Result)
		if err != nil {
			return err
		}
		return record(vmEvent)
	}), nil
}

// dockerHandler carries images between the host's Docker and the VM's for
// the pinned repositories, recording every push in the VM event log.
func (a *App) dockerHandler(cfg docker.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error, fail func(error)) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	host, err := docker.LoadHostConfig(homeDir)
	if err != nil {
		return nil, err
	}
	return docker.Handler(cfg, host, docker.CLI{}, docker.CacheFor(projectHash), func(event docker.PushEvent) error {
		vmEvent, err := runtimecfg.NewDockerVMEvent(projectHash, event.Repo, event.Tag, event.Digest)
		if err != nil {
			return err
		}
		return record(vmEvent)
	}, fail), nil
}

// epweaveHandler reaches ep_weave with the host credentials and serves
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
	return epweave.Handler(epweave.NewNotes(hostCfg, cfg, "isotty-"+projectHash, "isotty:"+cfg.Prefix), func(event epweave.WriteEvent) error {
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
	return github.Handler(hostCfg.Token, cfg, forgeRecorder(projectHash, "github", record)), nil
}

// gitlabHandler relays the pinned projects with the tokens of their
// instances, recording every push and write in the VM event log.
func (a *App) gitlabHandler(cfg gitlab.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	hostCfg, err := gitlab.LoadHostConfig(homeDir, projectHash)
	if err != nil {
		return nil, err
	}
	for _, repo := range cfg.SortedRepos() {
		host, _, _ := gitlab.Split(repo)
		if _, ok := hostCfg.Find(host); !ok {
			return nil, fmt.Errorf("project %s is pinned but %s lists no instance for %s", repo, hostCfg.Path(), host)
		}
	}
	return gitlab.Handler(hostCfg, cfg, forgeRecorder(projectHash, "gitlab", record)), nil
}

// bitbucketHandler relays the pinned repositories with the host token,
// recording every push and write in the VM event log.
func (a *App) bitbucketHandler(cfg bitbucket.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	hostCfg, err := bitbucket.LoadHostConfig(homeDir)
	if err != nil {
		return nil, err
	}
	return bitbucket.Handler(hostCfg.Token, cfg, forgeRecorder(projectHash, "bitbucket", record)), nil
}

// redmineHandler relays reads of the pinned Redmine projects with the
// host key, every answer filtered with the project's salt and recorded in
// the VM event log before the VM gets it. The record carries the answer,
// which can outgrow a command-line argument, so it goes over stdin.
func (a *App) redmineHandler(cfg redmine.ProjectConfig, conn vmcfg.GCPConnection, projectHash string) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	hostCfg, err := redmine.LoadHostConfig(homeDir)
	if err != nil {
		return nil, err
	}
	salt, err := redmine.LoadSalt(redmine.SaltPath(homeDir, projectHash))
	if err != nil {
		return nil, err
	}
	record := func(delivery redmine.Delivery) error {
		marks, err := json.Marshal(delivery.Marks)
		if err != nil {
			return err
		}
		event, err := runtimecfg.NewRedmineVMEvent(projectHash, delivery.Target, delivery.Content, marks)
		if err != nil {
			return err
		}
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		return vmcfg.RunGCPRemoteCommandWithInput(conn, runtimecfg.VMEventInputCommand(), bytes.NewReader(data))
	}
	handler, err := redmine.Handler(hostCfg, cfg, salt, record)
	if err != nil {
		return nil, fmt.Errorf("redmine: %w", err)
	}
	return handler, nil
}

func forgeRecorder(projectHash, kind string, record func(runtimecfg.VMEventRecord) error) func(forge.WriteEvent) error {
	return func(event forge.WriteEvent) error {
		vmEvent, err := runtimecfg.NewForgeVMEvent(projectHash, kind, event.Operation, event.Repo, event.Target)
		if err != nil {
			return err
		}
		return record(vmEvent)
	}
}

// googleHandler issues tokens for the pinned service account from the key on
// the host, recording every mint in the VM event log.
func (a *App) googleHandler(cfg google.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	issuer, err := google.LoadIssuer(homeDir, cfg)
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

// awsHandler hands out credentials of the pinned role, assumed with the
// profile this host recorded, recording every assumption in the VM event log.
func (a *App) awsHandler(cfg aws.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	hostCfg, err := aws.LoadHostConfig(homeDir, projectHash)
	if err != nil {
		return nil, err
	}
	return aws.Handler(aws.NewIssuer(cfg, hostCfg, projectHash), func(event aws.IssueEvent) error {
		vmEvent, err := runtimecfg.NewAWSVMEvent(projectHash, event.RoleARN, event.Session, event.Expiration)
		if err != nil {
			return err
		}
		return record(vmEvent)
	}), nil
}

// gasHandler runs functions of the pinned Apps Script projects with the
// host logins, recording every run in the VM event log.
func (a *App) gasHandler(cfg gas.ProjectConfig, projectHash string, record func(runtimecfg.VMEventRecord) error) (http.Handler, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return nil, err
	}
	creds, err := gas.LoadCredentials(homeDir, cfg)
	if err != nil {
		return nil, err
	}
	return gas.Handler(gas.NewRunner(cfg, creds), func(event gas.RunEvent) error {
		vmEvent, err := runtimecfg.NewGASVMEvent(projectHash, event.Account, event.Script, event.Function, event.Parameters, event.Result)
		if err != nil {
			return err
		}
		return record(vmEvent)
	}), nil
}

// attachShell says what the attach session runs in the VM.
type attachShell struct {
	Tmux bool
}

func attachShellFor(homeDir string, noTmux bool) (attachShell, error) {
	cfg, err := LoadAttachConfig(homeDir)
	if err != nil {
		return attachShell{}, err
	}
	return attachShell{Tmux: cfg.UseTmux() && !noTmux}, nil
}

const tmuxSession = "isotty"

func buildAttachSSHArgs(conn vmcfg.GCPConnection, workspacePath string, shell attachShell) []string {
	instanceTarget := fmt.Sprintf("%s.%s.%s", conn.InstanceName, conn.Zone, conn.ProjectID)
	if conn.User != "" {
		instanceTarget = conn.User + "@" + instanceTarget
	}
	return []string{
		"-F", conn.SSHConfigPath,
		"-tt",
		// Detect a dead peer within ~45s, so a dropped terminal is noticed
		// and attached again instead of lingering until TCP gives up.
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		instanceTarget, "cd " + workspacePath + " && " + attachShellCommand(workspacePath, shell),
	}
}

// attachShellCommand is what the attach runs once in the workspace.
//
// The session's shell gets BROWSER, so an agent's own open or `gh` opens on
// the host through the gateway the daemon holds while the VM runs. In tmux
// the variable goes into the session environment as well, for the windows
// opened later.
//
// With tmux, a terminal attaches to the session named `isotty`, created
// when missing. One that attaches while another is attached joins it as a
// grouped session, which shares the windows but has its own current
// window, and goes away when that terminal detaches.
func attachShellCommand(workspacePath string, shell attachShell) string {
	env := "BROWSER=" + browser.ClientPath + " "
	if !shell.Tmux {
		return env + "exec ${SHELL:-/bin/bash} -l"
	}
	create := env + "exec tmux new-session -A -s " + tmuxSession + " -c " + workspacePath +
		" ';' set-environment -t " + tmuxSession + " BROWSER " + browser.ClientPath
	join := "exec tmux new-session -t " + tmuxSession + " -c " + workspacePath + " ';' set-option destroy-unattached on"
	return "if tmux has-session -t " + tmuxSession + " 2>/dev/null && [ -n \"$(tmux list-clients -t " + tmuxSession + ")\" ]; then " + join + "; else " + create + "; fi"
}

// deleteChildren removes the VM's child VMs and the firewall rules of any
// child already gone; they live only as long as the VM runs, and nothing
// about them is kept. With record, each deletion goes into the VM event log.
func (a *App) deleteChildren(conn vmcfg.GCPConnection, record bool) error {
	children, err := vmcfg.ListChildInstances(conn)
	if err != nil {
		return fmt.Errorf("list child VMs: %w", err)
	}
	for _, c := range children {
		if err := a.runPhase("Deleting child VM %s", func() error {
			return vmcfg.DeleteChildInstance(conn, c.Instance, a.debug)
		}, c.Instance); err != nil {
			return fmt.Errorf("delete child VM: %w", err)
		}
		if !record {
			continue
		}
		event, err := runtimecfg.NewChildVMEvent(hashOf(conn), "delete", c.Name, c.Instance, c.MachineType, "", c.ExternalIP, "deleted")
		if err != nil {
			return err
		}
		if err := runtimecfg.RecordVMEvent(a.auditTarget(conn), event, a.debug); err != nil {
			return fmt.Errorf("record child-delete event: %w", err)
		}
	}
	if err := vmcfg.DeleteOrphanRules(conn, a.debug); err != nil {
		return fmt.Errorf("delete child VM firewall rules: %w", err)
	}
	return nil
}

// hashOf is the project hash an instance name carries.
func hashOf(conn vmcfg.GCPConnection) string {
	return strings.TrimPrefix(conn.InstanceName, "isotty-")
}

// warnDNSRecords names the records the VM created that still exist; the
// human decides whether they go, so `down` only says they are there.
func (a *App) warnDNSRecords(projectHash string) error {
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	tracked, err := dns.LoadTracked(homeDir, projectHash)
	if err != nil || len(tracked) == 0 {
		return err
	}
	remaining, err := dns.Remaining(dns.NewClient(), tracked)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return nil
	}
	fmt.Fprintln(a.stderr, "warning: DNS records this project created still exist; `isotty host dns show` lists them:")
	for _, record := range remaining {
		fmt.Fprintf(a.stderr, "  %s %s %s\n", record.Type, record.Name, strings.Join(record.Rrdatas, " "))
	}
	return nil
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

	conn := vmcfg.GCPConnection{
		ProjectID:    state.GCPProjectID,
		Zone:         state.Zone,
		InstanceName: state.InstanceName,
	}
	status, err := vmcfg.GCPInstanceStatus(state.GCPProjectID, state.Zone, state.InstanceName)
	if err != nil {
		return fmt.Errorf("check instance: %w", err)
	}
	exists := status != ""

	// The login archive is written into the synced .isotty/, so sync stops
	// first; a VM about to be deleted has no use for it.
	if err := a.runPhase("Stopping sync session", func() error {
		return terminateMutagenSession(state)
	}); err != nil {
		return fmt.Errorf("terminate sync session: %w", err)
	}
	if exists {
		// Logins and the audit log are read over SSH, so a stopped VM boots
		// once more before it goes.
		switch status {
		case vmcfg.GCPStatusRunning:
		case vmcfg.GCPStatusTerminated:
			if err := a.runPhase("Starting VM %s to save its state", func() error {
				return vmcfg.StartGCPInstance(conn, a.debug)
			}, state.InstanceName); err != nil {
				return fmt.Errorf("start instance: %w", err)
			}
			if err := a.runPhase("Waiting for SSH", func() error {
				return vmcfg.WaitForGCPSSH(conn)
			}); err != nil {
				return err
			}
		default:
			return fmt.Errorf("instance %s is %s; wait for it to settle before `isotty down`", state.InstanceName, status)
		}
		// The opeco session the VM kept dies with it; closing it takes its
		// card off the devices now instead of a day later.
		if err := a.auditTarget(conn).Run(opeco.SessionCommand("close"), a.debug); err != nil {
			fmt.Fprintf(a.stderr, "warning: the VM's opeco session was not closed; its card stays until it expires: %v\n", err)
		}
		if err := a.saveAgentAuth(state); err != nil {
			if !*force {
				return fmt.Errorf("%w\nhint: `isotty down --force` removes the VM without saving; sync is stopped until the next `isotty up`", err)
			}
			fmt.Fprintf(a.stderr, "warning: agent logins were not saved: %v\n", err)
		}
		// Before the archive, so the deletions are in it.
		if err := a.deleteChildren(conn, true); err != nil {
			return err
		}
		if _, err := a.archiveAudit(state); err != nil {
			if !*force {
				return fmt.Errorf("%w\nhint: `isotty down --force` removes the VM without the archive; sync is stopped until the next `isotty up`", err)
			}
			fmt.Fprintf(a.stderr, "warning: audit logs were not archived: %v\n", err)
		}
	}

	if exists {
		if err := a.runPhase("Deleting VM %s", func() error {
			return vmcfg.DeleteGCPInstance(conn, a.debug)
		}, state.InstanceName); err != nil {
			return fmt.Errorf("delete instance: %w", err)
		}
	} else if err := a.deleteChildren(conn, false); err != nil {
		// A parent deleted by hand may still have children.
		return err
	}

	if err := a.warnDNSRecords(state.ProjectHash); err != nil {
		fmt.Fprintf(a.stderr, "warning: could not check the DNS records this project created: %v\n", err)
	}

	if err := a.runPhase("Removing local state", func() error {
		// What passed between the VM and its registries is of no use
		// without the VM. An image that could not be let go of is named
		// here for the human, and the state goes all the same.
		if err := docker.CacheFor(state.ProjectHash).Remove(context.Background(), docker.CLI{}); err != nil {
			fmt.Fprintf(a.stderr, "warning: what the host kept of this project's Docker images was not all let go of: %v\n", err)
		}
		return DeleteState(state.ProjectHash)
	}); err != nil {
		return fmt.Errorf("remove state: %w", err)
	}
	// The daemon lets go of a project whose state is gone; told now, it
	// does so at once instead of at its next look. The project's wakes go:
	// the conversations they would resume went with the VM.
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	if err := wake.Forget(homeDir, state.ProjectHash); err != nil {
		return err
	}
	if _, err := daemonStatus(homeDir); err == nil {
		if err := reloadProject(homeDir, state.ProjectHash); err != nil {
			fmt.Fprintf(a.stderr, "warning: the isotty daemon was not told the project is gone; it lets go at its next look: %v\n", err)
		}
	}

	fmt.Fprintf(a.stdout, "IsoTTY environment removed.\n")
	return nil
}

// runStop stops the VM for a while, overnight say, and keeps everything
// else: the disk, the local state and the project config. The next `up`
// starts it again without a bootstrap.
func (a *App) runStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("stop does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	state, err := LoadStateForProject(projectPath)
	if err != nil {
		return err
	}

	status, err := vmcfg.GCPInstanceStatus(state.GCPProjectID, state.Zone, state.InstanceName)
	if err != nil {
		return fmt.Errorf("check instance: %w", err)
	}
	if status == "" {
		return fmt.Errorf("instance %s was not found; `isotty down` removes the local state", state.InstanceName)
	}
	conn := vmcfg.GCPConnection{
		ProjectID:    state.GCPProjectID,
		Zone:         state.Zone,
		InstanceName: state.InstanceName,
	}
	// Children go even when the parent was stopped by other means; their
	// deletion is recorded only while the parent can take the record.
	if err := a.deleteChildren(conn, status == vmcfg.GCPStatusRunning); err != nil {
		return err
	}
	if status != vmcfg.GCPStatusRunning {
		return fmt.Errorf("instance %s is %s, not running", state.InstanceName, status)
	}

	// The last local edits reach the VM before it goes down, and the session
	// does not spend the night reconnecting to a VM that is not there.
	if err := a.runPhase("Flushing sync session", func() error {
		return flushMutagenSession(state)
	}); err != nil {
		return fmt.Errorf("flush sync session: %w", err)
	}
	if err := a.runPhase("Stopping sync session", func() error {
		return terminateMutagenSession(state)
	}); err != nil {
		return fmt.Errorf("terminate sync session: %w", err)
	}
	if err := a.auditTarget(conn).Run(opeco.SessionCommand("status", "VM stopped (isotty stop)"), a.debug); err != nil {
		fmt.Fprintf(a.stderr, "warning: the VM's opeco session was not told of the stop: %v\n", err)
	}
	if err := a.runPhase("Stopping VM %s", func() error {
		return vmcfg.StopGCPInstance(conn, a.debug)
	}, state.InstanceName); err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}

	fmt.Fprintf(a.stdout, "IsoTTY environment stopped; `isotty up` starts it again.\n")
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

func (a *App) ensureEnvironment(cfg Config, syncMode string, ignoreRunning bool) (State, error) {
	state := NewState(cfg, syncMode)
	previous, stateErr := LoadStateForProject(cfg.ProjectPath)
	hasState := stateErr == nil
	if stateErr != nil && !IsStateNotFoundError(stateErr) {
		return State{}, stateErr
	}
	if hasState {
		// Another project or zone would find no VM there and make a second
		// one, leaving the first without a state pointing at it.
		if previous.GCPProjectID != cfg.GCPProjectID || previous.Zone != cfg.Zone {
			return State{}, fmt.Errorf("the VM of this project is in %s/%s, but ISOTTY_GCP_PROJECT and ISOTTY_GCP_ZONE name %s/%s; run `isotty down` before moving it, or leave them unset to use where it is", previous.GCPProjectID, previous.Zone, cfg.GCPProjectID, cfg.Zone)
		}
		state.OpecoQRPort = previous.OpecoQRPort
	}
	if err := saveGCPContext(cfg.HomeDir, cfg.ProjectHash, gcpContext{Project: cfg.GCPProjectID, Zone: cfg.Zone}); err != nil {
		return State{}, fmt.Errorf("remember the GCP project and zone: %w", err)
	}
	if opeco.Active(cfg.Runtime.Agents, cfg.Opeco) && state.OpecoQRPort == 0 {
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
	status, err := vmcfg.GCPInstanceStatus(cfg.GCPProjectID, cfg.Zone, state.InstanceName)
	if err != nil {
		return State{}, fmt.Errorf("check instance: %w", err)
	}
	if !hasState && status != "" {
		return State{}, fmt.Errorf("instance %s already exists but no local IsoTTY state was found; remove the VM explicitly", state.InstanceName)
	}
	switch status {
	case "", vmcfg.GCPStatusRunning, vmcfg.GCPStatusTerminated:
	default:
		return State{}, fmt.Errorf("instance %s is %s; wait for it to settle before `isotty up`", state.InstanceName, status)
	}

	e := &upEnv{
		a:          a,
		cfg:        cfg,
		state:      &state,
		conn:       vmcfg.GCPConnection{ProjectID: state.GCPProjectID, Zone: state.Zone, InstanceName: state.InstanceName},
		status:     status,
		ignoreLeft: ignoreRunning,
	}
	if e.homeArchive, e.hasHome, err = runtimecfg.HomeFilesArchive(cfg.HomeDir); err != nil {
		return State{}, err
	}
	if e.archives, err = runtimecfg.ListAuditArchives(cfg.ProjectPath); err != nil {
		return State{}, err
	}
	for _, agent := range runtimecfg.SavedAgents() {
		archive, ok, err := runtimecfg.LoadAgentAuth(cfg.HomeDir, cfg.ProjectPath, agent)
		if err != nil {
			return State{}, err
		}
		if !ok {
			continue
		}
		paths, err := runtimecfg.SavedAgentPaths(agent, archive)
		if err != nil {
			return State{}, err
		}
		e.auths = append(e.auths, savedAuth{agent: agent, archive: archive, paths: paths})
	}
	if e.postInstall, err = runtimecfg.HasPostInstallScript(cfg.ProjectPath); err != nil {
		return State{}, err
	}
	if err := a.runUpSteps(e); err != nil {
		return State{}, err
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
		CaptureTo: func(command string, w io.Writer) error {
			return vmcfg.CaptureGCPRemoteCommandTo(conn, command, w)
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
