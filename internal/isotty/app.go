package isotty

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultSyncMode        = "two-way-safe"
	oneWaySafeSyncMode     = "one-way-safe"
	twoWaySafeSyncMode     = "two-way-safe"
	defaultGCPMachineType  = "e2-standard-4"
	defaultGCPDiskSize     = "50GB"
	defaultGCPImageProject = "ubuntu-os-cloud"
	defaultGCPImageFamily  = "ubuntu-2404-lts-amd64"
	defaultWorkspacePath   = "/workspace"
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
	case "forward":
		return a.runForward(args[1:])
	case "runtime":
		return a.runRuntime(args[1:])
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
	fmt.Fprintln(a.stdout, "  isotty [--debug] up [PATH] [--sync one-way-safe|two-way-safe] [--no-attach]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] attach")
	fmt.Fprintln(a.stdout, "  isotty [--debug] down")
	fmt.Fprintln(a.stdout, "  isotty [--debug] audit logs [-f]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward add <name> --local-port <port> --remote-port <port>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward remove <name>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime apt add <package>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime apt remove <package>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime apt list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime gcp show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime gcp set [--machine-type <type>] [--boot-disk-size <size>] [--image-family <family>] [--image-project <project>]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime node set <major>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime node show")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime agent add <name>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime agent remove <name>...")
	fmt.Fprintln(a.stdout, "  isotty [--debug] runtime agent list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] status")
	fmt.Fprintln(a.stdout, "  isotty [--debug] version")
}

func (a *App) runUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	syncMode := fs.String("sync", defaultSyncMode, "synchronization mode")
	noAttach := fs.Bool("no-attach", false, "do not attach after preparing the environment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateSyncMode(*syncMode); err != nil {
		return err
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
	return a.attachToState(projectPath, state)
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

	events, err := queryAuditLogs(state, "boot")
	if err != nil {
		return err
	}
	a.printAuditEvents(events)
	return nil
}

func (a *App) runAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("attach does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	state, err := LoadStateForProject(projectPath)
	if err != nil {
		return err
	}

	return a.attachToState(projectPath, state)
}

func (a *App) attachToState(projectPath string, state State) error {
	forwardCfg, err := LoadForwardConfig(projectPath)
	if err != nil {
		return err
	}

	sshArgs := buildAttachSSHArgs(state, forwardCfg)
	names := SortedForwardNames(forwardCfg)

	if len(names) > 0 {
		a.phase("Attaching to %s with %d forwards", state.InstanceName, len(names))
	} else {
		a.phase("Attaching to %s", state.InstanceName)
	}

	startEvent, err := newAttachVMEvent(state, "attach-start", len(names), "")
	if err != nil {
		return err
	}
	if err := recordVMEvent(state, startEvent, a.debug); err != nil {
		return fmt.Errorf("record attach-start event: %w", err)
	}

	attachErr := RunInteractiveCommand("", os.Environ(), "gcloud", sshArgs...)

	result := "ok"
	if attachErr != nil {
		result = attachErr.Error()
	}
	endEvent, err := newAttachVMEvent(state, "attach-end", len(names), result)
	if err != nil {
		if attachErr != nil {
			return fmt.Errorf("attach session failed: %v; resolve attach-end event context: %w", attachErr, err)
		}
		return err
	}
	if err := recordVMEvent(state, endEvent, a.debug); err != nil {
		if attachErr != nil {
			return fmt.Errorf("attach session failed: %v; record attach-end event: %w", attachErr, err)
		}
		return fmt.Errorf("record attach-end event: %w", err)
	}
	return attachErr
}

func buildAttachSSHArgs(state State, forwardCfg ForwardConfig) []string {
	sshArgs := []string{
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
		"--ssh-flag=-t",
	}
	for _, name := range SortedForwardNames(forwardCfg) {
		forward := forwardCfg.Forwards[name]
		sshArgs = append(sshArgs, fmt.Sprintf("--ssh-flag=-L 127.0.0.1:%d:127.0.0.1:%d", forward.LocalPort, forward.RemotePort))
	}
	sshArgs = append(sshArgs, "--command", fmt.Sprintf("cd %s && exec ${SHELL:-/bin/bash} -l", defaultWorkspacePath))
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

	exists, err := gcloudInstanceExists(state.GCPProjectID, state.Zone, state.InstanceName)
	if err != nil {
		return fmt.Errorf("check instance: %w", err)
	}
	if exists {
		if err := a.runPhase("Deleting VM %s", func() error {
			return RunCommand("", os.Environ(), a.debug, "gcloud",
				"compute", "instances", "delete", state.InstanceName,
				"--quiet",
				"--project", state.GCPProjectID,
				"--zone", state.Zone,
			)
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
		return errors.New("runtime requires a subcommand: apt, node, or agent")
	}

	switch args[0] {
	case "apt":
		return a.runRuntimeApt(args[1:])
	case "gcp":
		return a.runRuntimeGCP(args[1:])
	case "node":
		return a.runRuntimeNode(args[1:])
	case "agent":
		return a.runRuntimeAgent(args[1:])
	default:
		return fmt.Errorf("unknown runtime subcommand %q", args[0])
	}
}

func (a *App) runRuntimeGCP(args []string) error {
	if len(args) == 0 {
		return errors.New("runtime gcp requires a subcommand: show or set")
	}

	switch args[0] {
	case "show":
		return a.runRuntimeGCPShow(args[1:])
	case "set":
		return a.runRuntimeGCPSet(args[1:])
	default:
		return fmt.Errorf("unknown runtime gcp subcommand %q", args[0])
	}
}

func (a *App) runRuntimeGCPShow(args []string) error {
	fs := flag.NewFlagSet("runtime gcp show", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime gcp show does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	cfg, err := RuntimeGCPVMConfig(projectPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "machine_type: %s\n", *cfg.MachineType)
	fmt.Fprintf(a.stdout, "boot_disk_size: %s\n", *cfg.BootDiskSize)
	fmt.Fprintf(a.stdout, "image_family: %s\n", *cfg.ImageFamily)
	fmt.Fprintf(a.stdout, "image_project: %s\n", *cfg.ImageProject)
	return nil
}

func (a *App) runRuntimeGCPSet(args []string) error {
	fs := flag.NewFlagSet("runtime gcp set", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	machineType := fs.String("machine-type", "", "GCP machine type")
	bootDiskSize := fs.String("boot-disk-size", "", "GCP boot disk size")
	imageFamily := fs.String("image-family", "", "GCP image family")
	imageProject := fs.String("image-project", "", "GCP image project")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime gcp set does not accept positional arguments")
	}

	updates := gcpVMConfig{}
	if flagProvided(args, "--machine-type") {
		updates.MachineType = machineType
	}
	if flagProvided(args, "--boot-disk-size") {
		updates.BootDiskSize = bootDiskSize
	}
	if flagProvided(args, "--image-family") {
		updates.ImageFamily = imageFamily
	}
	if flagProvided(args, "--image-project") {
		updates.ImageProject = imageProject
	}
	if updates.MachineType == nil && updates.BootDiskSize == nil && updates.ImageFamily == nil && updates.ImageProject == nil {
		return errors.New("runtime gcp set requires at least one flag")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	if err := SetRuntimeGCPVMConfig(projectPath, updates); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "Updated GCP VM config")
	return nil
}

func (a *App) runRuntimeApt(args []string) error {
	if len(args) == 0 {
		return errors.New("runtime apt requires a subcommand: add, remove, or list")
	}

	switch args[0] {
	case "add":
		return a.runRuntimeAptAdd(args[1:])
	case "remove":
		return a.runRuntimeAptRemove(args[1:])
	case "list":
		return a.runRuntimeAptList(args[1:])
	default:
		return fmt.Errorf("unknown runtime apt subcommand %q", args[0])
	}
}

func (a *App) runRuntimeAptAdd(args []string) error {
	fs := flag.NewFlagSet("runtime apt add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime apt add requires at least one package")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	if err := AddRuntimeAptPackages(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Added %d apt package(s)\n", len(fs.Args()))
	return nil
}

func (a *App) runRuntimeAptRemove(args []string) error {
	fs := flag.NewFlagSet("runtime apt remove", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime apt remove requires at least one package")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	if err := RemoveRuntimeAptPackages(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Removed %d apt package(s)\n", len(fs.Args()))
	return nil
}

func (a *App) runRuntimeAptList(args []string) error {
	fs := flag.NewFlagSet("runtime apt list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime apt list does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	packages, err := ListRuntimeAptPackages(projectPath)
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		fmt.Fprintln(a.stdout, "No apt packages configured.")
		return nil
	}
	for _, pkg := range packages {
		fmt.Fprintln(a.stdout, pkg)
	}
	return nil
}

func (a *App) runRuntimeNode(args []string) error {
	if len(args) == 0 {
		return errors.New("runtime node requires a subcommand: set or show")
	}

	switch args[0] {
	case "set":
		return a.runRuntimeNodeSet(args[1:])
	case "show":
		return a.runRuntimeNodeShow(args[1:])
	default:
		return fmt.Errorf("unknown runtime node subcommand %q", args[0])
	}
}

func (a *App) runRuntimeNodeSet(args []string) error {
	fs := flag.NewFlagSet("runtime node set", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("runtime node set requires exactly one major version")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	if err := SetRuntimeNodeVersion(projectPath, fs.Arg(0)); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Set Node.js major version to %s\n", fs.Arg(0))
	return nil
}

func (a *App) runRuntimeNodeShow(args []string) error {
	fs := flag.NewFlagSet("runtime node show", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime node show does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	version, err := RuntimeNodeVersion(projectPath)
	if err != nil {
		return err
	}
	if version == "" {
		fmt.Fprintln(a.stdout, "No Node.js version configured.")
		return nil
	}
	fmt.Fprintln(a.stdout, version)
	return nil
}

func (a *App) runRuntimeAgent(args []string) error {
	if len(args) == 0 {
		return errors.New("runtime agent requires a subcommand: add, remove, or list")
	}

	switch args[0] {
	case "add":
		return a.runRuntimeAgentAdd(args[1:])
	case "remove":
		return a.runRuntimeAgentRemove(args[1:])
	case "list":
		return a.runRuntimeAgentList(args[1:])
	default:
		return fmt.Errorf("unknown runtime agent subcommand %q", args[0])
	}
}

func (a *App) runRuntimeAgentAdd(args []string) error {
	fs := flag.NewFlagSet("runtime agent add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime agent add requires at least one agent")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	if err := AddRuntimeAgents(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Added %d agent(s)\n", len(fs.Args()))
	return nil
}

func (a *App) runRuntimeAgentRemove(args []string) error {
	fs := flag.NewFlagSet("runtime agent remove", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("runtime agent remove requires at least one agent")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	if err := RemoveRuntimeAgents(projectPath, fs.Args()); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Removed %d agent(s)\n", len(fs.Args()))
	return nil
}

func (a *App) runRuntimeAgentList(args []string) error {
	fs := flag.NewFlagSet("runtime agent list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("runtime agent list does not accept arguments")
	}

	projectPath, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	agents, err := ListRuntimeAgents(projectPath)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		fmt.Fprintln(a.stdout, "No agents configured.")
		return nil
	}
	for _, agent := range agents {
		fmt.Fprintln(a.stdout, agent)
	}
	return nil
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
	path, err := execLookPath(name)
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

	exists, err := gcloudInstanceExists(cfg.GCPProjectID, cfg.Zone, state.InstanceName)
	if err != nil {
		return State{}, fmt.Errorf("check instance: %w", err)
	}
	if !exists {
		if err := a.runPhase("Creating VM %s", func() error {
			return createInstance(cfg, state.InstanceName, a.debug)
		}, state.InstanceName); err != nil {
			return State{}, err
		}
		created = true
	} else {
		a.phase("Reusing VM %s", state.InstanceName)
	}

	if err := a.runPhase("Waiting for SSH", func() error {
		return waitForSSH(state)
	}); err != nil {
		return State{}, err
	}
	if created {
		if err := a.runPhase(bootstrapLabel(state), func() error {
			return bootstrapWorkspace(state, a.debug)
		}); err != nil {
			return State{}, err
		}
	} else {
		a.phase("Skipping bootstrap for existing VM %s", state.InstanceName)
	}
	if err := a.runPhase("Refreshing SSH config", func() error {
		return refreshSSHConfig(state, a.debug)
	}); err != nil {
		return State{}, err
	}
	if err := a.runPhase("Preparing SSH wrappers", func() error {
		return ensureSSHWrappers(state)
	}); err != nil {
		return State{}, err
	}
	if err := a.runPhase("Configuring audit", func() error {
		return configureAudit(state, a.debug)
	}); err != nil {
		return State{}, err
	}
	if err := a.runPhase("Starting sync session", func() error {
		return recreateMutagenSession(state, a.debug)
	}); err != nil {
		return State{}, err
	}
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
		events, err := queryAuditLogs(state, "recent")
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

func (a *App) printAuditEvents(events []AuditEvent) {
	for _, event := range events {
		a.printAuditEvent(event)
	}
}

func (a *App) printAuditEvent(event AuditEvent) {
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

func validateSyncMode(mode string) error {
	if _, ok := supportedSyncModes[mode]; ok {
		return nil
	}
	return fmt.Errorf("unsupported sync mode %q", mode)
}

func flagProvided(args []string, name string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			return true
		}
		if len(args[i]) > len(name) && strings.HasPrefix(args[i], name+"=") {
			return true
		}
	}
	return false
}
