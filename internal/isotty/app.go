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

	switch args[0] {
	case "up":
		return a.runUp(args[1:])
	case "attach":
		return a.runAttach(args[1:])
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
	fmt.Fprintln(a.stdout, "  isotty up [PATH] [--sync one-way-safe|two-way-safe]")
	fmt.Fprintln(a.stdout, "  isotty attach")
	fmt.Fprintln(a.stdout, "  isotty down")
	fmt.Fprintln(a.stdout, "  isotty status")
	fmt.Fprintln(a.stdout, "  isotty version")
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

	return RunInteractiveCommand("", os.Environ(), "gcloud",
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
	)
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

	if err := terminateMutagenSession(state); err != nil {
		return fmt.Errorf("terminate sync session: %w", err)
	}

	exists, err := gcloudInstanceExists(state.GCPProjectID, state.Zone, state.InstanceName)
	if err != nil {
		return fmt.Errorf("check instance: %w", err)
	}
	if exists {
		if err := RunInteractiveCommand("", os.Environ(), "gcloud",
			"compute", "instances", "delete", state.InstanceName,
			"--quiet",
			"--project", state.GCPProjectID,
			"--zone", state.Zone,
		); err != nil {
			return fmt.Errorf("delete instance: %w", err)
		}
	}

	if err := DeleteState(state.ProjectHash); err != nil {
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
		if err := createInstance(cfg, state.InstanceName); err != nil {
			return State{}, err
		}
	}

	if err := waitForSSH(state); err != nil {
		return State{}, err
	}
	if err := bootstrapWorkspace(state); err != nil {
		return State{}, err
	}
	if err := refreshSSHConfig(state); err != nil {
		return State{}, err
	}
	if err := ensureSSHWrappers(state); err != nil {
		return State{}, err
	}
	if err := recreateMutagenSession(state); err != nil {
		return State{}, err
	}
	if err := SaveState(state); err != nil {
		return State{}, fmt.Errorf("save state: %w", err)
	}

	return state, nil
}

func validateSyncMode(mode string) error {
	if _, ok := supportedSyncModes[mode]; ok {
		return nil
	}
	return fmt.Errorf("unsupported sync mode %q", mode)
}
