// Package gas runs functions of Apps Script projects from the VM as the
// human, through scripts.run, for the scripts the human pinned. The human's
// OAuth credentials stay on the host. See docs/GAS.md.
package gas

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

var (
	scriptPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	emailPattern  = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
)

// identityScopes let the login report which Google account authorized; the
// human sees that account in `show` and the VM in `status`.
var identityScopes = []string{"openid", "email"}

// Client is the OAuth client Google Cloud Console issued for a desktop
// application, read from its client_secret JSON. scripts.run accepts only a
// token issued to a client of the script's own Cloud project.
type Client struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AuthURI      string `json:"auth_uri"`
	TokenURI     string `json:"token_uri"`
}

// Credential is what the login left on the host: the client and the refresh
// token Google issued to it for one account and a set of scopes. It never
// enters the VM.
type Credential struct {
	Client
	Account      string   `json:"account"`
	RefreshToken string   `json:"refresh_token"`
	Scopes       []string `json:"scopes"`
}

// Script is one pinned Apps Script project: which client reaches it, as
// which account, with which scopes. The scopes are written as the script's
// manifest lists them. Client and scopes belong to the script because each
// script's Cloud project issues its own clients.
type Script struct {
	ID      string   `json:"id"`
	Client  string   `json:"client"`
	Account string   `json:"account"`
	Scopes  []string `json:"scopes"`
}

// ProjectConfig lists the scripts the VM may run functions of.
type ProjectConfig struct {
	Scripts []Script `json:"scripts"`
}

func CredentialDir(homeDir string) string {
	return filepath.Join(homeDir, "gas")
}

func CredentialPath(homeDir, clientID string) string {
	return filepath.Join(CredentialDir(homeDir), clientID+".yaml")
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "gas.yaml")
}

// ReadClient parses a client_secret JSON file. It must not be inside the
// project, which is synced into the VM.
func ReadClient(projectPath, path string) (Client, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Client{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	if rel, err := filepath.Rel(projectPath, absPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Client{}, fmt.Errorf("%s is inside the project, which is synced into the VM; move the client file out of the project first", path)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return Client{}, fmt.Errorf("read %s: %w", path, err)
	}
	client, err := ParseClient(data)
	if err != nil {
		return Client{}, fmt.Errorf("%s: %w", path, err)
	}
	return client, nil
}

func ParseClient(data []byte) (Client, error) {
	var file struct {
		Installed *Client `json:"installed"`
		Web       *Client `json:"web"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return Client{}, fmt.Errorf("parse OAuth client file: %w", err)
	}
	if file.Installed == nil {
		if file.Web != nil {
			return Client{}, errors.New("the OAuth client is a web application; create a desktop application client, whose file has an \"installed\" section")
		}
		return Client{}, errors.New("not an OAuth client file: no \"installed\" section")
	}
	client := *file.Installed
	for name, value := range map[string]string{"client_id": client.ClientID, "client_secret": client.ClientSecret, "auth_uri": client.AuthURI, "token_uri": client.TokenURI} {
		if strings.TrimSpace(value) == "" {
			return Client{}, fmt.Errorf("OAuth client file does not define installed.%s", name)
		}
	}
	return client, nil
}

func SaveCredential(homeDir string, cred Credential) error {
	data, err := yaml.Marshal(cred)
	if err != nil {
		return fmt.Errorf("marshal credential: %w", err)
	}
	if err := os.MkdirAll(CredentialDir(homeDir), 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	return os.WriteFile(CredentialPath(homeDir, cred.ClientID), data, 0o600)
}

// LoadCredential reports ok=false when no login has been done with the client.
func LoadCredential(homeDir, clientID string) (Credential, bool, error) {
	path := CredentialPath(homeDir, clientID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Credential{}, false, nil
		}
		return Credential{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	var cred Credential
	if err := yaml.UnmarshalStrict(data, &cred); err != nil {
		return Credential{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if cred.ClientID != clientID {
		return Credential{}, false, fmt.Errorf("%s holds the credential of client %s, not %s", path, cred.ClientID, clientID)
	}
	return cred, true, nil
}

// Covers reports whether the credential was granted every scope. The
// identity scopes are never asked about: Google reports them under other
// names, and the account in the credential proves they were granted.
func (c Credential) Covers(scopes []string) bool {
	for _, scope := range scopes {
		if !slices.Contains(c.Scopes, scope) {
			return false
		}
	}
	return true
}

// LoadCredentials returns, per client, the host credential the pinned
// scripts run with: a login by the pinned account covering the scopes of
// every pinned script that uses the client.
func LoadCredentials(homeDir string, cfg ProjectConfig) (map[string]Credential, error) {
	creds := map[string]Credential{}
	for _, script := range cfg.Scripts {
		cred, ok := creds[script.Client]
		if !ok {
			loaded, exists, err := LoadCredential(homeDir, script.Client)
			if err != nil {
				return nil, err
			}
			if !exists {
				return nil, fmt.Errorf("%s does not exist; run `isotty host gas add --script %s --client <file> ...` to log in with client %s", CredentialPath(homeDir, script.Client), script.ID, script.Client)
			}
			cred = loaded
			creds[script.Client] = cred
		}
		if cred.Account != script.Account {
			return nil, fmt.Errorf("%s was authorized by %s, but script %s is pinned to %s; run `isotty host gas add` for it again to log in as that account", CredentialPath(homeDir, script.Client), cred.Account, script.ID, script.Account)
		}
		if !cred.Covers(script.Scopes) {
			return nil, fmt.Errorf("%s was not granted every scope pinned for script %s; run `isotty host gas add` for it again to log in for them", CredentialPath(homeDir, script.Client), script.ID)
		}
	}
	return creds, nil
}

// LoadProjectConfig reports enabled=false when the project has no Apps Script config.
func LoadProjectConfig(projectPath string) (ProjectConfig, bool, error) {
	path := ProjectConfigPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfig{}, false, nil
		}
		return ProjectConfig{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := ParseProjectConfig(data)
	if err != nil {
		return ProjectConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

func ParseProjectConfig(data []byte) (ProjectConfig, error) {
	var cfg ProjectConfig
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return ProjectConfig{}, err
	}
	if err := cfg.validate(); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

func SaveProjectConfig(projectPath string, cfg ProjectConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal gas config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create gas config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// RemoveProjectConfig is what an empty list means: no config at all.
func RemoveProjectConfig(projectPath string) error {
	if err := os.Remove(ProjectConfigPath(projectPath)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", ProjectConfigPath(projectPath), err)
	}
	return nil
}

func (c ProjectConfig) validate() error {
	if len(c.Scripts) == 0 {
		return errors.New("scripts must name at least one Apps Script project")
	}
	seen := map[string]bool{}
	for _, script := range c.Scripts {
		if err := script.validate(); err != nil {
			return err
		}
		if seen[script.ID] {
			return fmt.Errorf("script %s is listed twice", script.ID)
		}
		seen[script.ID] = true
	}
	return nil
}

func (s Script) validate() error {
	if err := ValidateScript(s.ID); err != nil {
		return err
	}
	if strings.TrimSpace(s.Client) == "" {
		return fmt.Errorf("script %s names no client", s.ID)
	}
	if !emailPattern.MatchString(s.Account) {
		return fmt.Errorf("script %s has invalid account %q: expected an email address", s.ID, s.Account)
	}
	if len(s.Scopes) == 0 {
		return fmt.Errorf("script %s must name at least one scope", s.ID)
	}
	for _, scope := range s.Scopes {
		if err := ValidateScope(scope); err != nil {
			return err
		}
	}
	return nil
}

func ValidateScript(script string) error {
	if !scriptPattern.MatchString(script) {
		return fmt.Errorf("invalid script %q: expected an Apps Script project id", script)
	}
	return nil
}

// ValidateScope accepts a scope as the script's manifest lists it, the full
// URL; Apps Script scopes do not all share one prefix.
func ValidateScope(scope string) error {
	if !strings.HasPrefix(scope, "https://") || strings.ContainsAny(scope, " \t\r\n") {
		return fmt.Errorf("invalid scope %q: expected the full scope URL as the script's manifest lists it", scope)
	}
	return nil
}

// Find returns the pinned script with the id.
func (c ProjectConfig) Find(id string) (Script, bool) {
	for _, script := range c.Scripts {
		if script.ID == id {
			return script, true
		}
	}
	return Script{}, false
}

// Put replaces the script with the same id or appends it.
func (c *ProjectConfig) Put(script Script) {
	for i := range c.Scripts {
		if c.Scripts[i].ID == script.ID {
			c.Scripts[i] = script
			return
		}
	}
	c.Scripts = append(c.Scripts, script)
}

// Delete reports whether a script with the id was pinned.
func (c *ProjectConfig) Delete(id string) bool {
	before := len(c.Scripts)
	c.Scripts = slices.DeleteFunc(c.Scripts, func(s Script) bool { return s.ID == id })
	return len(c.Scripts) != before
}

// ClientScopes is the union of the scopes of every script that uses the
// client, sorted: what one login with that client must be granted.
func (c ProjectConfig) ClientScopes(clientID string) []string {
	var scopes []string
	for _, script := range c.Scripts {
		if script.Client == clientID {
			scopes = append(scopes, script.Scopes...)
		}
	}
	slices.Sort(scopes)
	return slices.Compact(scopes)
}

// LoginScopes is what a login asks for: the scopes plus those that
// identify the account.
func LoginScopes(scopes []string) []string {
	all := append(slices.Clone(scopes), identityScopes...)
	slices.Sort(all)
	return slices.Compact(all)
}

func (c ProjectConfig) sorted() []Script {
	scripts := make([]Script, 0, len(c.Scripts))
	for _, script := range c.Scripts {
		script.Scopes = slices.Clone(script.Scopes)
		slices.Sort(script.Scopes)
		scripts = append(scripts, script)
	}
	slices.SortFunc(scripts, func(a, b Script) int { return strings.Compare(a.ID, b.ID) })
	return scripts
}
