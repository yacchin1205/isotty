package host

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/dns"
	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// LoadDNSProjectConfig returns the synced project config once it has been
// checked against the pin this host recorded at `isotty host dns add`, or
// nil when the project has no pinned name.
func LoadDNSProjectConfig(projectPath, projectHash, homeDir string) (*dns.ProjectConfig, error) {
	synced, enabled, err := dns.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := dns.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runDNS(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host dns requires a subcommand: add, remove, or show")
	}
	switch args[0] {
	case "add":
		return runDNSAdd(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "remove":
		return runDNSRemove(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runDNSShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host dns subcommand %q", args[0])
	}
}

// loadDNSPin returns what this host has accepted so far. add and remove
// build on the pin, not on the synced file, which the VM may have edited.
func loadDNSPin(homeDir, projectHash string) (dns.ProjectConfig, error) {
	var pinned dns.ProjectConfig
	if _, err := pin.Load(homeDir, projectHash, dns.PinKind, &pinned); err != nil {
		return dns.ProjectConfig{}, err
	}
	return pinned, nil
}

// saveDNS writes the synced file and the pin together; an empty list is no
// config at all.
func saveDNS(projectPath, projectHash, homeDir string, cfg dns.ProjectConfig) error {
	if len(cfg.Zones) == 0 {
		if err := dns.RemoveProjectConfig(projectPath); err != nil {
			return err
		}
		return pin.Remove(homeDir, projectHash, dns.PinKind)
	}
	if err := dns.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	return pin.Save(homeDir, projectHash, dns.PinKind, cfg)
}

// runDNSAdd pins names in the Cloud DNS zones of a project. The
// zone of each name is the one serving it, found with the host's gcloud
// login; --managed-zone names it when two zones serve the same domain. The
// provider is the project's; with Cloud DNS the only one, add sets it.
func runDNSAdd(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host dns add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "Google Cloud project of the managed zones")
	managedZone := fs.String("managed-zone", "", "managed zone to use when more than one serves the name")
	var names specs
	fs.Var(&names, "name", "record name the VM may change, with everything under it (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host dns add does not accept arguments")
	}
	if *project == "" || len(names) == 0 {
		return errors.New("host dns add requires --project and at least one --name")
	}
	for _, name := range names {
		if err := dns.ValidateName(name); err != nil {
			return err
		}
	}
	zones, err := dns.NewClient().ManagedZones(*project)
	if err != nil {
		return fmt.Errorf("project %s: %w", *project, err)
	}
	cfg, err := loadDNSPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	cfg.Provider = dns.ProviderCloudDNS
	for _, name := range names {
		zone, err := dns.ChooseZone(zones, name, *managedZone)
		if err != nil {
			return err
		}
		cfg.Put(dns.Zone{Project: *project, ManagedZone: zone.Name, Names: []string{name}})
		fmt.Fprintf(stdout, "Pinned %s and the names under it in %s/%s (%s)\n", name, *project, zone.Name, zone.DNSName)
	}
	if err := dns.ValidateProjectConfig(cfg); err != nil {
		return err
	}
	if err := saveDNS(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

func runDNSRemove(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host dns remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "pinned record name to remove")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *name == "" {
		return errors.New("host dns remove requires --name <record name> and no arguments")
	}
	cfg, err := loadDNSPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	if !cfg.Delete(*name) {
		return fmt.Errorf("name %s is not pinned for this project", *name)
	}
	if err := saveDNS(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed %s\n", *name)
	if len(cfg.Zones) == 0 {
		fmt.Fprintln(stdout, "No name is pinned for this project any more.")
	}
	return nil
}

// runDNSShow prints the agreement and the records the VM created that
// still exist, read back from the zones.
func runDNSShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host dns show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host dns show does not accept arguments")
	}
	cfg, err := LoadDNSProjectConfig(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	tracked, err := dns.LoadTracked(homeDir, projectHash)
	if err != nil {
		return err
	}
	if cfg == nil && len(tracked) == 0 {
		fmt.Fprintln(stdout, "No name is pinned for this project.")
		return nil
	}
	if cfg != nil {
		fmt.Fprintf(stdout, "provider: %s\nzones:\n", cfg.Provider)
		for _, zone := range cfg.Zones {
			fmt.Fprintf(stdout, "  %s/%s: %s\n", zone.Project, zone.ManagedZone, strings.Join(zone.Names, ", "))
		}
		fmt.Fprintf(stdout, "pinned: %s\n", pin.Path(homeDir, projectHash, dns.PinKind))
	}
	if len(tracked) == 0 {
		return nil
	}
	remaining, err := dns.Remaining(dns.NewClient(), tracked)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		fmt.Fprintln(stdout, "No record the VM created is left.")
		return nil
	}
	fmt.Fprintln(stdout, "records the VM created that still exist:")
	for _, record := range remaining {
		fmt.Fprintf(stdout, "  %s %s %d %s\n", record.Type, record.Name, record.TTL, strings.Join(record.Rrdatas, " "))
	}
	return nil
}
