package isotty

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	defaultSyncMode      = "one-way-safe"
	developmentSyncMode  = "two-way-safe"
	defaultMachineType   = "e2-standard-4"
	defaultDiskSize      = "50GB"
	defaultImageProject  = "ubuntu-os-cloud"
	defaultImageFamily   = "ubuntu-2404-lts-amd64"
	defaultWorkspacePath = "/workspace"
)

var supportedSyncModes = map[string]struct{}{
	defaultSyncMode:     {},
	developmentSyncMode: {},
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
	fmt.Fprintln(a.stdout, "  isotty [--debug] up [PATH] [--sync one-way-safe|two-way-safe]")
	fmt.Fprintln(a.stdout, "  isotty [--debug] attach")
	fmt.Fprintln(a.stdout, "  isotty [--debug] down")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward add <name> --local-port <port> --remote-port <port>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward list")
	fmt.Fprintln(a.stdout, "  isotty [--debug] forward remove <name>")
	fmt.Fprintln(a.stdout, "  isotty [--debug] status")
	fmt.Fprintln(a.stdout, "  isotty [--debug] version")
}

func (a *App) runUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	syncMode := fs.String("sync", defaultSyncMode, "synchronization mode")
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

	forwardCfg, err := LoadForwardConfig(projectPath)
	if err != nil {
		return err
	}

	sshArgs := []string{
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
	}
	names := SortedForwardNames(forwardCfg)
	for _, name := range names {
		forward := forwardCfg.Forwards[name]
		sshArgs = append(sshArgs, "--ssh-flag", fmt.Sprintf("-L 127.0.0.1:%d:127.0.0.1:%d", forward.LocalPort, forward.RemotePort))
	}

	if len(names) > 0 {
		a.phase("Attaching to %s with %d forwards", state.InstanceName, len(names))
	} else {
		a.phase("Attaching to %s", state.InstanceName)
	}
	return RunInteractiveCommand("", os.Environ(), "gcloud", sshArgs...)
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
	} else {
		a.phase("Reusing VM %s", state.InstanceName)
	}

	if err := a.runPhase("Waiting for SSH", func() error {
		return waitForSSH(state)
	}); err != nil {
		return State{}, err
	}
	if len(state.AptPackages) > 0 {
		if err := a.runPhase("Bootstrapping workspace and installing packages", func() error {
			return bootstrapWorkspace(state, a.debug)
		}); err != nil {
			return State{}, err
		}
	} else {
		if err := a.runPhase("Bootstrapping workspace", func() error {
			return bootstrapWorkspace(state, a.debug)
		}); err != nil {
			return State{}, err
		}
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

func validateSyncMode(mode string) error {
	if _, ok := supportedSyncModes[mode]; ok {
		return nil
	}
	return fmt.Errorf("unsupported sync mode %q", mode)
}
