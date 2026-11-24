// Package aws hands the VM short-lived credentials of an IAM role the human
// chose, assumed on the host with the human's AWS login. See docs/AWS.md.
package aws

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"sigs.k8s.io/yaml"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// PinKind names the host pin of the project config; see package pin.
const PinKind = "aws"

var (
	roleARNPattern = regexp.MustCompile(`^arn:aws[a-z-]*:iam::([0-9]{12}):role/[A-Za-z0-9+=,.@_/-]{1,512}$`)
	profilePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

// ProjectConfig names the role the VM acts as; the account is the role's.
type ProjectConfig struct {
	RoleARN string `json:"role_arn"`
}

// HostConfig is which of the host's AWS logins assumes the role, the host's
// own matter and never synced. An empty profile is the aws CLI's default.
type HostConfig struct {
	Profile string `json:"profile,omitempty"`
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "aws.yaml")
}

func HostConfigPath(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+".aws-host.yaml")
}

// Account is the account in the role's ARN.
func (c ProjectConfig) Account() string {
	return roleARNPattern.FindStringSubmatch(c.RoleARN)[1]
}

func (c ProjectConfig) Validate() error {
	if !roleARNPattern.MatchString(c.RoleARN) {
		return fmt.Errorf("invalid role_arn %q: expected arn:aws:iam::<account>:role/<name>", c.RoleARN)
	}
	return nil
}

func (c HostConfig) Validate() error {
	if c.Profile != "" && !profilePattern.MatchString(c.Profile) {
		return fmt.Errorf("invalid profile %q", c.Profile)
	}
	return nil
}

// LoadProjectConfig reports enabled=false when the project has no AWS config.
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

// Save writes the synced file, the pin and the host config together.
func Save(projectPath, projectHash, homeDir string, cfg ProjectConfig, host HostConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := host.Validate(); err != nil {
		return err
	}
	if err := writeYAML(ProjectConfigPath(projectPath), cfg); err != nil {
		return err
	}
	if err := writeYAML(HostConfigPath(homeDir, projectHash), host); err != nil {
		return err
	}
	return pin.Save(homeDir, projectHash, PinKind, cfg)
}

// Remove forgets the role: the synced file, the pin and the host config.
func Remove(projectPath, projectHash, homeDir string) error {
	path := ProjectConfigPath(projectPath)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("AWS is not enabled for this project (%s does not exist)", path)
		}
		return fmt.Errorf("remove %s: %w", path, err)
	}
	if err := os.Remove(HostConfigPath(homeDir, projectHash)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return pin.Remove(homeDir, projectHash, PinKind)
}

func LoadHostConfig(homeDir, projectHash string) (HostConfig, error) {
	path := HostConfigPath(homeDir, projectHash)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HostConfig{}, fmt.Errorf("%s does not exist; run `isotty host aws enable` on this host", path)
		}
		return HostConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var host HostConfig
	if err := yaml.UnmarshalStrict(data, &host); err != nil {
		return HostConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return host, host.Validate()
}

// VerifyPinned returns the synced config only when it matches the host pin.
func VerifyPinned(homeDir, projectHash string, synced ProjectConfig) (ProjectConfig, error) {
	var pinned ProjectConfig
	ok, err := pin.Load(homeDir, projectHash, PinKind, &pinned)
	if err != nil {
		return ProjectConfig{}, err
	}
	if !ok {
		return ProjectConfig{}, fmt.Errorf("AWS is configured in .isotty/aws.yaml but not pinned on this host; run `isotty host aws enable` here to accept it")
	}
	if pinned.RoleARN != synced.RoleARN {
		return ProjectConfig{}, fmt.Errorf(
			".isotty/aws.yaml (%s) differs from what is pinned on this host (%s); the file is synced from the VM, so something in the VM may have edited it. Review it, then run `isotty host aws enable` again to accept the new value",
			synced.RoleARN, pinned.RoleARN,
		)
	}
	return synced, nil
}

func writeYAML(path string, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// SessionName names the role session after the project, so CloudTrail tells
// this project's calls from the human's own.
func SessionName(projectHash string) string {
	return "isotty-" + projectHash
}
