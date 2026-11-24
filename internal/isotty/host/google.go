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

// checkGoogle issues one token as the agreement asks, so that a service
// account the host cannot use is found at enable and not at attach.
var checkGoogle = func(homeDir string, cfg google.ProjectConfig) error {
	issuer, err := google.LoadIssuer(homeDir, cfg)
	if err != nil {
		return err
	}
	// The token stays on the host, so there is nothing to record.
	_, err = issuer.Issue(func(google.Token) error { return nil })
	return err
}

// runGoogleEnable takes the service account from --key or --impersonate, or
// keeps the one already pinned with its credential; the scopes are always
// written whole. Nothing is pinned until a token has been issued.
func runGoogleEnable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host google enable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyPath := fs.String("key", "", "service account key file to import")
	impersonate := fs.String("impersonate", "", "service account to impersonate with the host's gcloud login")
	var scopes specs
	fs.Var(&scopes, "scope", "scope without the https://www.googleapis.com/auth/ prefix (repeatable)")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: isotty host google enable [--key <service-account.json> | --impersonate <service-account>] --scope <scope>...

Lets the VM use Google APIs as the service account, through tokens the
host issues while attached. Once a service account is pinned, --scope
alone changes the scopes.

`+googleGuide)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host google enable does not accept arguments")
	}
	if len(scopes) == 0 {
		return errors.New("host google enable requires at least one --scope")
	}
	if *keyPath != "" && *impersonate != "" {
		return errors.New("host google enable takes --key or --impersonate, not both")
	}

	var cfg google.ProjectConfig
	switch {
	case *keyPath != "":
		imported, err := google.ImportKey(homeDir, projectPath, *keyPath)
		if err != nil {
			return err
		}
		cfg = google.ProjectConfig{ServiceAccount: imported, Credential: google.CredentialKey}
	case *impersonate != "":
		cfg = google.ProjectConfig{ServiceAccount: *impersonate, Credential: google.CredentialImpersonation}
	default:
		var pinned google.ProjectConfig
		ok, err := pin.Load(homeDir, projectHash, google.PinKind, &pinned)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("host google enable requires --key <file> or --impersonate <service-account> the first time")
		}
		cfg = google.ProjectConfig{ServiceAccount: pinned.ServiceAccount, Credential: pinned.CredentialKind()}
	}
	cfg.Scopes = scopes
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := checkGoogle(homeDir, cfg); err != nil {
		fmt.Fprint(stderr, googleGuide)
		return fmt.Errorf("no token could be issued as %s, so nothing was pinned: %w", cfg.ServiceAccount, err)
	}
	if err := google.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	if err := pin.Save(homeDir, projectHash, google.PinKind, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Enabled Google for this project as %s (%s)\n", cfg.ServiceAccount, cfg.Credential)
	fmt.Fprintf(stdout, "scopes: %s\n", strings.Join(cfg.Scopes, ", "))
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

const googleGuide = `Make the service account ready, in any Google Cloud project:

  1. Create it, or pick one:
       gcloud iam service-accounts create <name> --project <project>
  2. For --impersonate, let your gcloud account issue its tokens:
       gcloud iam service-accounts add-iam-policy-binding <service-account> \
           --member user:<your account> --role roles/iam.serviceAccountTokenCreator
     If the check says the IAM Service Account Credentials API is disabled,
     enable iamcredentials.googleapis.com in the project it names.
     For --key, create a key and keep it outside the project directory:
       gcloud iam service-accounts keys create <file> --iam-account <service-account>
     An organization created since 2024 forbids keys by default.
  3. What the agent may reach is what you share with the service account
     and the roles you grant it; grant those as you would for anyone.

`

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
	fmt.Fprintf(stdout, "service_account: %s\n", cfg.ServiceAccount)
	fmt.Fprintf(stdout, "credential: %s\n", cfg.CredentialKind())
	fmt.Fprintf(stdout, "scopes: %s\n", strings.Join(cfg.Scopes, ", "))
	fmt.Fprintf(stdout, "pinned: %s\n", pin.Path(homeDir, projectHash, google.PinKind))
	if cfg.CredentialKind() == google.CredentialKey {
		if _, err := google.LoadKey(homeDir, cfg.ServiceAccount); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "key: %s\n", google.KeyPath(homeDir, cfg.ServiceAccount))
	}
	return nil
}
