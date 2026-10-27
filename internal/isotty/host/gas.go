package host

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/browser"
	"github.com/yazawa/isotty/internal/isotty/host/gas"
	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// loginTimeout bounds how long `add` waits for the human to finish the
// authorization in the browser.
const loginTimeout = 10 * time.Minute

const gasAddGuide = `Usage: isotty host gas add --script <id>... --client <client_secret.json> --scope <url>...

Lets the VM run functions of the Apps Script projects as you, through
scripts.run, at their latest saved code. Google accepts that call only
with a token of a human who owns the script, issued by an OAuth client of
the script's own Cloud project, covering every scope the script uses. Each
script is pinned as its own entry with the client and scopes given; adding
a script that is already pinned replaces its entry.

--script <id>
    The script id, shown as "Script ID" in Project Settings and in the
    editor's URL; repeat to pin several scripts of the same Cloud project
    with the same scopes. You must own the script: the functions run at
    the latest saved code (devMode), which Google allows the owner only.
    The script must have been deployed as "API executable" at least once
    (Deploy > New deployment); until then the API cannot load it. add
    checks each script by asking the API for a function that does not
    exist, and refuses the pin when the script cannot be reached.

--client <client_secret.json>
    The JSON of an OAuth client of type "Desktop app" in the Cloud project
    the script is attached to. To get one:

      1. Attach the script to a standard Cloud project.
         Apps Script editor > Project Settings > Google Cloud Platform (GCP) Project
           https://script.google.com/home/projects/<script-id>/settings
         Enter the project NUMBER shown on the Cloud project's dashboard
           https://console.cloud.google.com/home/dashboard
         The default project Apps Script creates on its own does not qualify.

      2. In that Cloud project, enable the Apps Script API.
           https://console.cloud.google.com/apis/library/script.googleapis.com

      3. In that Cloud project, set up Google Auth Platform (APIs & Services >
         Google Auth Platform): a fresh project shows a "Get started" button
         on its Branding page. Under Audience, keep it in testing and add
         yourself as a test user.
           https://console.cloud.google.com/auth/branding
           https://console.cloud.google.com/auth/audience

      4. In that Cloud project, create an OAuth client of type "Desktop app"
         and download its JSON.
           https://console.cloud.google.com/auth/clients

    Keep the file outside the project directory, which is synced into the
    VM. The same file serves every script of that Cloud project.

--scope <url>
    A scope the script uses; repeat for each. Read them from the script's
    overview page under "Project OAuth Scopes" and pass every full URL.
      https://script.google.com/home/projects/<script-id>
    The token must cover all of them, not only those the functions you have
    in mind use.

The browser opens for your authorization the first time a client is used,
and again when the scopes of the scripts using it grow beyond what was
granted; log in as the account the VM should run the script as. If
scripts.run answers 403 although the projects match, turn on the Apps
Script API for your account:
  https://script.google.com/home/usersettings

Reference: https://developers.google.com/apps-script/api/how-tos/execute
`

// LoadGASProjectConfig returns the synced project config once it has been
// checked against the pin this host recorded by `isotty host gas add`, or
// nil when the project has no pinned script.
func LoadGASProjectConfig(projectPath, projectHash, homeDir string) (*gas.ProjectConfig, error) {
	synced, enabled, err := gas.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := gas.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runGAS(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host gas requires a subcommand: add, remove, clear, or show")
	}

	switch args[0] {
	case "add":
		return runGASAdd(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "remove":
		return runGASRemove(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "clear":
		return runGASClear(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runGASShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host gas subcommand %q", args[0])
	}
}

// loadGASPin returns what this host has accepted so far. add and remove
// build on the pin, not on the synced file, which the VM may have edited.
func loadGASPin(homeDir, projectHash string) (gas.ProjectConfig, error) {
	var pinned gas.ProjectConfig
	if _, err := pin.Load(homeDir, projectHash, gas.PinKind, &pinned); err != nil {
		return gas.ProjectConfig{}, err
	}
	return pinned, nil
}

// saveGAS writes the synced file and the pin together; an empty list is no
// config at all.
func saveGAS(projectPath, projectHash, homeDir string, cfg gas.ProjectConfig) error {
	if len(cfg.Scripts) == 0 {
		if err := gas.RemoveProjectConfig(projectPath); err != nil {
			return err
		}
		return pin.Remove(homeDir, projectHash, gas.PinKind)
	}
	if err := gas.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	return pin.Save(homeDir, projectHash, gas.PinKind, cfg)
}

func runGASAdd(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host gas add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	clientPath := fs.String("client", "", "OAuth desktop client file from the scripts' Cloud project")
	var scriptIDs, scopes specs
	fs.Var(&scriptIDs, "script", "Apps Script project id (repeatable)")
	fs.Var(&scopes, "scope", "scope the scripts use, the full URL as their manifests list it (repeatable)")
	fs.Usage = func() { fmt.Fprint(stderr, gasAddGuide) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host gas add does not accept arguments")
	}
	if len(scriptIDs) == 0 || *clientPath == "" || len(scopes) == 0 {
		return errors.New("host gas add requires at least one --script, --client and at least one --scope; `isotty host gas add -h` walks through the Google side")
	}
	for _, id := range scriptIDs {
		if err := gas.ValidateScript(id); err != nil {
			return err
		}
	}
	client, err := gas.ReadClient(projectPath, *clientPath)
	if err != nil {
		return err
	}
	cfg, err := loadGASPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	put := func(account string) {
		for _, id := range scriptIDs {
			cfg.Put(gas.Script{ID: id, Client: client.ClientID, Account: account, Scopes: scopes})
		}
	}
	put("")

	cred, ok, err := gas.LoadCredential(homeDir, client.ClientID)
	if err != nil {
		return err
	}
	if !ok || !cred.Covers(cfg.ClientScopes(client.ClientID)) {
		ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
		defer cancel()
		fmt.Fprintln(stdout, "Opening the browser to authorize the Apps Script client. Log in as the account the VM should run the script as.")
		cred, err = gas.Login(ctx, client, gas.LoginScopes(cfg.ClientScopes(client.ClientID)), func(url string) error {
			fmt.Fprintf(stdout, "If no browser opened, visit:\n%s\n", url)
			return browser.OpenInBrowser(ctx, url)
		})
		if err != nil {
			return err
		}
		if err := gas.SaveCredential(homeDir, cred); err != nil {
			return err
		}
	}
	put(cred.Account)
	for _, other := range cfg.Scripts {
		if other.Client == client.ClientID && other.Account != cred.Account {
			return fmt.Errorf("client %s is now authorized by %s, but script %s was pinned as %s; run `isotty host gas add` for it again, or log in as %s", client.ClientID, cred.Account, other.ID, other.Account, other.Account)
		}
	}
	runner := gas.NewRunner(cfg, map[string]gas.Credential{client.ClientID: cred})
	for _, id := range scriptIDs {
		script, _ := cfg.Find(id)
		if err := runner.Verify(script); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Verified %s: reachable through the Apps Script API\n", id)
	}
	if err := saveGAS(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Pinned Apps Script %s as %s\n", strings.Join(scriptIDs, ", "), cred.Account)
	fmt.Fprintf(stdout, "scopes: %s\n", strings.Join(scopes, ", "))
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

func runGASRemove(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host gas remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	scriptID := fs.String("script", "", "Apps Script project id to remove")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *scriptID == "" {
		return errors.New("host gas remove requires --script <id> and no arguments")
	}
	cfg, err := loadGASPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	if !cfg.Delete(*scriptID) {
		return fmt.Errorf("script %s is not pinned for this project", *scriptID)
	}
	if err := saveGAS(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed Apps Script %s. The login stays under ~/.isotty/gas/.\n", *scriptID)
	return nil
}

func runGASClear(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host gas clear", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host gas clear does not accept arguments")
	}
	if err := saveGAS(projectPath, projectHash, homeDir, gas.ProjectConfig{}); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Removed every pinned Apps Script. The logins stay under ~/.isotty/gas/.")
	return nil
}

func runGASShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host gas show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host gas show does not accept arguments")
	}
	cfg, err := LoadGASProjectConfig(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "No Apps Script is pinned for this project.")
		return nil
	}
	if _, err := gas.LoadCredentials(homeDir, *cfg); err != nil {
		return err
	}
	for _, script := range cfg.Scripts {
		fmt.Fprintf(stdout, "script: %s\n", script.ID)
		fmt.Fprintf(stdout, "  account: %s\n", script.Account)
		fmt.Fprintf(stdout, "  client: %s\n", script.Client)
		fmt.Fprintf(stdout, "  scopes: %s\n", strings.Join(script.Scopes, ", "))
	}
	fmt.Fprintf(stdout, "pinned: %s\n", pin.Path(homeDir, projectHash, gas.PinKind))
	return nil
}
