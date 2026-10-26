package host

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/google"
	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// LoadGoogleProjectConfig returns the synced project config once it has
// been checked against the pin this host recorded at `isotty host google
// enable`, or nil when the project has not enabled Google.
func LoadGoogleProjectConfig(projectPath, projectHash, homeDir string) (*google.ProjectConfig, error) {
	synced, enabled, err := google.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := google.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runGoogle(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host google requires a subcommand: enable, disable, or show")
	}

	switch args[0] {
	case "enable":
		return runGoogleEnable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "disable":
		return runGoogleDisable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runGoogleShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host google subcommand %q", args[0])
	}
}

// runGoogleEnable imports the key when --key is given and otherwise keeps
// the service account already pinned; the scopes are always written whole.
func runGoogleEnable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host google enable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyPath := fs.String("key", "", "service account key file to import; may be omitted once a key is pinned for this project")
	var scopes specs
	fs.Var(&scopes, "scope", "scope without the https://www.googleapis.com/auth/ prefix (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host google enable does not accept arguments")
	}
	if len(scopes) == 0 {
		return errors.New("host google enable requires at least one --scope")
	}

	var serviceAccount string
	if *keyPath != "" {
		imported, err := google.ImportKey(homeDir, projectPath, *keyPath)
		if err != nil {
			return err
		}
		serviceAccount = imported
	} else {
		var pinned google.ProjectConfig
		ok, err := pin.Load(homeDir, projectHash, google.PinKind, &pinned)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("host google enable requires --key <file> the first time")
		}
		serviceAccount = pinned.ServiceAccount
	}
	cfg := google.ProjectConfig{ServiceAccount: serviceAccount, Scopes: scopes}
	if _, err := google.LoadKey(homeDir, serviceAccount); err != nil {
		return err
	}
	if err := google.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	if err := pin.Save(homeDir, projectHash, google.PinKind, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Enabled Google for this project as %s\n", cfg.ServiceAccount)
	fmt.Fprintf(stdout, "scopes: %s\n", strings.Join(cfg.Scopes, ", "))
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

func runGoogleDisable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host google disable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host google disable does not accept arguments")
	}
	if err := google.RemoveProjectConfig(projectPath); err != nil {
		return err
	}
	if err := pin.Remove(homeDir, projectHash, google.PinKind); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Disabled Google for this project. The imported key stays under ~/.isotty/google/.")
	return nil
}

func runGoogleShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host google show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host google show does not accept arguments")
	}
	cfg, err := LoadGoogleProjectConfig(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "Google is not enabled for this project.")
		return nil
	}
	if _, err := google.LoadKey(homeDir, cfg.ServiceAccount); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "service_account: %s\n", cfg.ServiceAccount)
	fmt.Fprintf(stdout, "scopes: %s\n", strings.Join(cfg.Scopes, ", "))
	fmt.Fprintf(stdout, "pinned: %s\n", pin.Path(homeDir, projectHash, google.PinKind))
	fmt.Fprintf(stdout, "key: %s\n", google.KeyPath(homeDir, cfg.ServiceAccount))
	return nil
}
