package host

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/child"
	"github.com/yazawa/isotty/internal/isotty/host/pin"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

// LoadChildProjectConfig returns the synced project config once it has
// been checked against the pin this host recorded at `isotty host children
// enable`, or nil when the project allows no children.
func LoadChildProjectConfig(projectPath, projectHash, homeDir string) (*child.ProjectConfig, error) {
	synced, enabled, err := child.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := child.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runChildren(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host children requires a subcommand: enable, disable, or show")
	}
	switch args[0] {
	case "enable":
		return runChildrenEnable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "disable":
		return runChildrenDisable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runChildrenShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host children subcommand %q", args[0])
	}
}

// ports collects a repeatable port flag.
type ports []int

func (p *ports) String() string {
	parts := make([]string, 0, len(*p))
	for _, port := range *p {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ",")
}

func (p *ports) Set(v string) error {
	port, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("port %q is not a number", v)
	}
	*p = append(*p, port)
	return nil
}

// runChildrenEnable writes the whole agreement; running it again replaces
// it. Without flags a child may be what the project's own VM is (its
// machine type and disk, from vm.yaml or the defaults), one at a time, with
// the web ports open.
func runChildrenEnable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	vm, err := vmcfg.Load(projectPath)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("host children enable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	max := fs.Int("max", 1, "how many child VMs the project may have at once")
	diskSize := fs.String("boot-disk-size", *vm.GCP.BootDiskSize, "largest boot disk a child may have, for example 200GB")
	var machineTypes specs
	var expose ports
	fs.Var(&machineTypes, "machine-type", "machine type a child may have (repeatable); the project's VM type when omitted")
	fs.Var(&expose, "expose", "port a child may open to the world (repeatable); 80 and 443 when omitted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host children enable does not accept arguments")
	}
	if len(machineTypes) == 0 {
		machineTypes = specs{*vm.GCP.MachineType}
	}
	if len(expose) == 0 {
		expose = ports{80, 443}
	}
	cfg := child.ProjectConfig{Max: *max, MachineTypes: machineTypes, MaxBootDiskSize: *diskSize, Expose: expose}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := child.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	if err := pin.Save(homeDir, projectHash, child.PinKind, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Enabled child VMs for this project: %s\n", cfg.Summary())
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

func runChildrenDisable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host children disable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host children disable does not accept arguments")
	}
	if err := child.RemoveProjectConfig(projectPath); err != nil {
		return err
	}
	if err := pin.Remove(homeDir, projectHash, child.PinKind); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Disabled child VMs for this project. Children that exist stay until `isotty stop` or `down`.")
	return nil
}

func runChildrenShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host children show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host children show does not accept arguments")
	}
	cfg, err := LoadChildProjectConfig(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "Child VMs are not enabled for this project.")
		return nil
	}
	fmt.Fprintf(stdout, "max: %d\nmachine_types: %s\nmax_boot_disk_size: %s\nexpose: %s\n", cfg.Max, strings.Join(cfg.MachineTypes, ", "), cfg.MaxBootDiskSize, (*ports)(&cfg.Expose).String())
	fmt.Fprintf(stdout, "pinned: %s\n", pin.Path(homeDir, projectHash, child.PinKind))
	return nil
}
