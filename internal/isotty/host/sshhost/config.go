// Package sshhost lets the VM log in over SSH to machines on the host's
// network that the human named: the host gateway relays a TCP stream to
// each, and the VM holds a key of the project's for each, made on the host.
package sshhost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
	"sigs.k8s.io/yaml"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// PinKind names the host pin of the project config; see package pin.
const PinKind = "ssh"

// Host is a machine the VM may log in to: where the host reaches it, the
// account, and the host keys the human accepted at `add`.
type Host struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	User     string   `json:"user"`
	HostKeys []string `json:"host_keys"`
}

type ProjectConfig struct {
	Hosts map[string]Host `json:"hosts"`
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "ssh.yaml")
}

// LoadProjectConfig reports enabled=false when the project names no machine.
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
	if err := cfg.Validate(); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

// Save writes the synced file and the pin together; no machine is no
// config at all.
func Save(projectPath, projectHash, homeDir string, cfg ProjectConfig) error {
	if len(cfg.Hosts) == 0 {
		if err := os.Remove(ProjectConfigPath(projectPath)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", ProjectConfigPath(projectPath), err)
		}
		return pin.Remove(homeDir, projectHash, PinKind)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal ssh config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create ssh config directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return pin.Save(homeDir, projectHash, PinKind, cfg)
}

// LoadPin returns what this host has accepted so far. add and remove build
// on the pin, not on the synced file, which the VM may have edited.
func LoadPin(homeDir, projectHash string) (ProjectConfig, error) {
	var pinned ProjectConfig
	if _, err := pin.Load(homeDir, projectHash, PinKind, &pinned); err != nil {
		return ProjectConfig{}, err
	}
	if pinned.Hosts == nil {
		pinned.Hosts = map[string]Host{}
	}
	return pinned, nil
}

// VerifyPinned returns the synced config only when it matches the host pin.
func VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	var pinned ProjectConfig
	ok, err := pin.Load(homeDir, projectHash, PinKind, &pinned)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, errors.New("SSH hosts are configured in .isotty/ssh.yaml but not pinned on this host; run `isotty host ssh add` here to accept them")
	}
	if !pinned.equal(synced) {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/ssh.yaml (%s) differs from what is pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Remove the machines that should not be there with `isotty host ssh remove`, or add them again, to pin the file anew",
			synced.Summary(), pinned.Summary(),
		)
	}
	return synced, nil
}

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	userPattern = regexp.MustCompile(`^[a-z_][a-zA-Z0-9_.-]{0,63}$`)
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$`)
)

// ValidateName checks a machine's name, which is the host name the agent
// gives `ssh` in the VM.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("name %q must be lowercase letters, digits and hyphens", name)
	}
	return nil
}

// ValidateAddress checks where the machine is and the account, which is
// all a machine has before its first login.
func (h Host) ValidateAddress() error {
	if !hostPattern.MatchString(h.Host) {
		return fmt.Errorf("host %q must be a host name or an address", h.Host)
	}
	if h.Port < 1 || h.Port > 65535 {
		return fmt.Errorf("port %d is out of range", h.Port)
	}
	if !userPattern.MatchString(h.User) {
		return fmt.Errorf("user %q is not an account name", h.User)
	}
	return nil
}

func (h Host) Validate() error {
	if err := h.ValidateAddress(); err != nil {
		return err
	}
	if len(h.HostKeys) == 0 {
		return errors.New("host_keys must hold the machine's host key")
	}
	for _, key := range h.HostKeys {
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err != nil {
			return fmt.Errorf("host key %q: %w", key, err)
		}
	}
	return nil
}

func (c ProjectConfig) Validate() error {
	if len(c.Hosts) == 0 {
		return errors.New("hosts must name at least one machine")
	}
	for name, h := range c.Hosts {
		if err := ValidateName(name); err != nil {
			return err
		}
		if err := h.Validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// HostKeyAlgorithms are the algorithms ssh may verify the recorded host
// keys with: those of their types and no other, so that ssh asks the
// machine for one of them, an old machine's RSA key included.
func (h Host) HostKeyAlgorithms() string {
	var algorithms []string
	for _, key := range h.HostKeys {
		keyType := strings.Fields(key)[0]
		names := []string{keyType}
		if keyType == ssh.KeyAlgoRSA {
			names = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
		}
		for _, name := range names {
			if !slices.Contains(algorithms, name) {
				algorithms = append(algorithms, name)
			}
		}
	}
	return strings.Join(algorithms, ",")
}

// Names returns the machines in a stable order.
func (c ProjectConfig) Names() []string {
	names := make([]string, 0, len(c.Hosts))
	for name := range c.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c ProjectConfig) equal(other ProjectConfig) bool {
	if len(c.Hosts) != len(other.Hosts) {
		return false
	}
	for name, h := range c.Hosts {
		o, ok := other.Hosts[name]
		if !ok || h.Host != o.Host || h.Port != o.Port || h.User != o.User || !slices.Equal(h.HostKeys, o.HostKeys) {
			return false
		}
	}
	return true
}

// Summary names each machine with the account and the address.
func (c ProjectConfig) Summary() string {
	var parts []string
	for _, name := range c.Names() {
		parts = append(parts, name+"="+c.Hosts[name].Address())
	}
	return strings.Join(parts, ", ")
}

// Address is the account and the place, as user@host:port.
func (h Host) Address() string {
	return fmt.Sprintf("%s@%s:%d", h.User, h.Host, h.Port)
}

// KeyDir is where the host keeps the project's private keys, one file per
// machine, outside the synced project.
func KeyDir(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+".ssh")
}

func KeyPath(homeDir, projectHash, name string) string {
	return filepath.Join(KeyDir(homeDir, projectHash), name)
}
