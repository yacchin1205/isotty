package isotty

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

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
	case "forward":
		return a.runForward(args[1:])
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
	fmt.Fprintln(a.stdout, "  isotty [--debug] attach [--target <id>] [--user <name>] [--no-forward]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] down")
	fmt.Fprintln(a.stdout, "  isotty [--debug] audit logs [-f]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward add <name> --local-port <port> --remote-port <port>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward remove <name>")
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
	noForward := fs.Bool("no-forward", false, "attach without loading or applying port forwards")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("attach does not accept arguments")
	}
	if *targetID != "" {
		return a.attachToTarget(*targetID, *user, *noForward)
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

	return a.attachToState(projectPath, state, *user, *noForward)
}

func (a *App) attachToTarget(targetID, user string, noForward bool) error {
	target, err := vmcfg.ParseGCPID(targetID)
	if err != nil {
		return err
	}
	conn := vmcfg.GCPConnection{
		ProjectID:    target.ProjectID,
		Zone:         target.Zone,
		InstanceName: target.InstanceName,
	}

	forwardCfg := ForwardConfig{Forwards: map[string]Forward{}}
	if !noForward {
		data, err := vmcfg.FetchGCPProjectFile(conn, ".isotty/forward.yaml")
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else {
			forwardCfg, err = ParseForwardConfig(data)
			if err != nil {
				return fmt.Errorf("parse remote forward config: %w", err)
			}
		}
	}

	workspacePath, err := vmcfg.GetGCPWorkspacePath(conn)
	if err != nil {
		return err
	}
	projectHash, err := vmcfg.GetGCPProjectHash(conn)
	if err != nil {
		return err
	}
	return a.attachWithConnection(conn, user, projectHash, workspacePath, forwardCfg)
}

func (a *App) attachToState(projectPath string, state State, user string, noForward bool) error {
	forwardCfg := ForwardConfig{Forwards: map[string]Forward{}}
	if !noForward {
		var err error
		forwardCfg, err = LoadForwardConfig(projectPath)
		if err != nil {
			return err
		}
	}

	conn := vmcfg.GCPConnection{
		ProjectID:    state.GCPProjectID,
		Zone:         state.Zone,
		InstanceName: state.InstanceName,
	}
	workspacePath, err := vmcfg.GetGCPWorkspacePath(conn)
	if err != nil {
		return err
	}
	return a.attachWithConnection(conn, user, state.ProjectHash, workspacePath, forwardCfg)
}

func (a *App) attachWithConnection(conn vmcfg.GCPConnection, user, projectHash, workspacePath string, forwardCfg ForwardConfig) error {
	sshArgs := buildAttachSSHArgs(conn, user, workspacePath, forwardCfg)
	names := SortedForwardNames(forwardCfg)

	if len(names) > 0 {
		a.phase("Attaching to %s with %d forwards", conn.InstanceName, len(names))
	} else {
		a.phase("Attaching to %s", conn.InstanceName)
	}

	startEvent, err := runtimecfg.NewAttachVMEvent(projectHash, "attach-start", len(names), "")
	if err != nil {
		return err
	}
	target := a.auditTarget(conn)
	if err := runtimecfg.RecordVMEvent(target, startEvent, a.debug); err != nil {
		return fmt.Errorf("record attach-start event: %w", err)
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
	return attachErr
}

func buildAttachSSHArgs(conn vmcfg.GCPConnection, user, workspacePath string, forwardCfg ForwardConfig) []string {
	instanceTarget := conn.InstanceName
	if user != "" {
		instanceTarget = user + "@" + instanceTarget
	}
	sshArgs := []string{
		"compute", "ssh", instanceTarget,
		"--project", conn.ProjectID,
		"--zone", conn.Zone,
		"--ssh-flag=-t",
		"--ssh-flag=-t",
	}
	for _, name := range SortedForwardNames(forwardCfg) {
		forward := forwardCfg.Forwards[name]
		sshArgs = append(sshArgs, fmt.Sprintf("--ssh-flag=-L 127.0.0.1:%d:127.0.0.1:%d", forward.LocalPort, forward.RemotePort))
	}
	sshArgs = append(sshArgs, "--command", fmt.Sprintf("cd %s && exec ${SHELL:-/bin/bash} -l", workspacePath))
	return sshArgs
}

func (a *App) runDown(args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
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

	if err := a.runPhase("Stopping sync session", func() error {
		return terminateMutagenSession(state)
	}); err != nil {
		return fmt.Errorf("terminate sync session: %w", err)
	}

	exists, err := vmcfg.GCPInstanceExists(state.GCPProjectID, state.Zone, state.InstanceName)
	if err != nil {
		return fmt.Errorf("check instance: %w", err)
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

	output, err := CaptureCommand("", state.MutagenEnv(), "mutagen", "sync", "list", "-l", state.SessionName)
	if err != nil {
		return fmt.Errorf("query mutagen session: %w", err)
	}
	fmt.Fprint(a.stdout, output)
	return nil
}

func (a *App) runForward(args []string) error {
	if len(args) == 0 {
		return errors.New("forward requires a subcommand: add, list, or remove")
	}

	switch args[0] {
	case "add":
		return a.runForwardAdd(args[1:])
	case "list":
		return a.runForwardList(args[1:])
	case "remove":
		return a.runForwardRemove(args[1:])
	default:
		return fmt.Errorf("unknown forward subcommand %q", args[0])
	}
}

func (a *App) runRuntime(args []string) error {
	if len(args) == 0 {
		return errors.New("runtime requires a subcommand: apt, service, post-install, node, or agent")
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
		return runtimecfg.RunAgent(projectPath, args[1:], a.stdout, a.stderr)
	default:
		return fmt.Errorf("unknown runtime subcommand %q", args[0])
	}
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

func (a *App) runForwardAdd(args []string) error {
	if len(args) == 0 {
		return errors.New("forward add requires a name")
	}
	name := args[0]

	fs := flag.NewFlagSet("forward add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	localPort := fs.Int("local-port", 0, "local port")
	remotePort := fs.Int("remote-port", 0, "remote port")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("forward add accepts exactly one name")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	forward := Forward{LocalPort: *localPort, RemotePort: *remotePort}
	if err := AddForward(projectPath, name, forward); err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "Added forward %s (%d -> %d)\n", name, forward.LocalPort, forward.RemotePort)
	return nil
}

func (a *App) runForwardList(args []string) error {
	fs := flag.NewFlagSet("forward list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("forward list does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	cfg, err := LoadForwardConfig(projectPath)
	if err != nil {
		return err
	}
	names := SortedForwardNames(cfg)
	if len(names) == 0 {
		fmt.Fprintln(a.stdout, "No forwards configured.")
		return nil
	}
	for _, name := range names {
		forward := cfg.Forwards[name]
		fmt.Fprintf(a.stdout, "%s: localhost:%d -> remote:%d\n", name, forward.LocalPort, forward.RemotePort)
	}
	return nil
}

func (a *App) runForwardRemove(args []string) error {
	fs := flag.NewFlagSet("forward remove", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("forward remove requires a name")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	name := fs.Arg(0)
	if err := RemoveForward(projectPath, name); err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "Removed forward %s\n", name)
	return nil
}

func (a *App) printDependencyVersion(name string, args []string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		fmt.Fprintf(a.stdout, "%s: not found\n", name)
		return nil
	}

	fmt.Fprintf(a.stdout, "%s: %s\n", name, path)

	output, err := CaptureCommand("", os.Environ(), name, args...)
	if err != nil {
		return fmt.Errorf("check %s version: %w", name, err)
	}

	fmt.Fprintln(a.stdout, "")
	fmt.Fprint(a.stdout, output)
	if len(output) > 0 && output[len(output)-1] != '\n' {
		fmt.Fprintln(a.stdout, "")
	}
	return nil
}

func (a *App) ensureEnvironment(cfg Config, syncMode string) (State, error) {
	state := NewState(cfg, syncMode)
	created := false
	gcpConfig := cfg.VM.GCP
	_, stateErr := LoadStateForProject(cfg.ProjectPath)
	hasState := stateErr == nil
	if stateErr != nil && !IsStateNotFoundError(stateErr) {
		return State{}, stateErr
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
		fmt.Fprintf(a.stdout, "%s exec %s\n", timestamp, event.Command)
	case "connect":
		if event.Address != "" {
			fmt.Fprintf(a.stdout, "%s connect %s\n", timestamp, event.Address)
			return
		}
		fmt.Fprintf(a.stdout, "%s connect %s\n", timestamp, event.Executable)
	default:
		fmt.Fprintf(a.stdout, "%s %s\n", timestamp, event.RawSummary())
	}
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
