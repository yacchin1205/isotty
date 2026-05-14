package isotty

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

type Config struct {
	ProjectPath  string
	ProjectHash  string
	GCPProjectID string
	Zone         string
	HomeDir      string
	MachineType  string
	BootDiskSize string
	ImageFamily  string
	ImageProject string
	AptPackages  []string
	NodeVersion  string
	Agents       []string
}

type agentConfig struct {
	Agents map[string]map[string]any `json:"agents"`
}

type vmConfig struct {
	Provider *string     `json:"provider"`
	GCP      gcpVMConfig `json:"gcp"`
}

type gcpVMConfig struct {
	MachineType  *string `json:"machine_type"`
	BootDiskSize *string `json:"boot_disk_size"`
	ImageFamily  *string `json:"image_family"`
	ImageProject *string `json:"image_project"`
}

var nodeMajorVersionPattern = regexp.MustCompile(`^[0-9]+$`)

func LoadConfig(projectPath string) (Config, error) {
	projectHash := hashProjectPath(projectPath)

	homeDir, err := isottyHome()
	if err != nil {
		return Config{}, fmt.Errorf("resolve isotty home: %w", err)
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create isotty home: %w", err)
	}

	projectID, err := resolveSetting("ISOTTY_GCP_PROJECT", []string{"gcloud", "config", "get-value", "project"})
	if err != nil {
		return Config{}, fmt.Errorf("resolve GCP project: %w", err)
	}
	zone, err := resolveSetting("ISOTTY_GCP_ZONE", []string{"gcloud", "config", "get-value", "compute/zone"})
	if err != nil {
		return Config{}, fmt.Errorf("resolve GCP zone: %w", err)
	}
	aptPackages, err := loadAptPackages(projectPath)
	if err != nil {
		return Config{}, err
	}
	nodeVersion, err := loadNodeVersion(projectPath)
	if err != nil {
		return Config{}, err
	}
	agents, err := loadAgents(projectPath)
	if err != nil {
		return Config{}, err
	}
	vmShape, err := loadVMConfig(projectPath)
	if err != nil {
		return Config{}, err
	}

	return Config{
		ProjectPath:  projectPath,
		ProjectHash:  projectHash,
		GCPProjectID: projectID,
		Zone:         zone,
		HomeDir:      homeDir,
		MachineType:  *vmShape.MachineType,
		BootDiskSize: *vmShape.BootDiskSize,
		ImageFamily:  *vmShape.ImageFamily,
		ImageProject: *vmShape.ImageProject,
		AptPackages:  aptPackages,
		NodeVersion:  nodeVersion,
		Agents:       agents,
	}, nil
}

func hashProjectPath(projectPath string) string {
	sum := sha256.Sum256([]byte(projectPath))
	return hex.EncodeToString(sum[:])[:12]
}

func isottyHome() (string, error) {
	if home := strings.TrimSpace(os.Getenv("ISOTTY_HOME")); home != "" {
		return home, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".isotty"), nil
}

func resolveSetting(envKey string, fallbackCommand []string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return value, nil
	}

	output, err := exec.Command(fallbackCommand[0], fallbackCommand[1:]...).Output()
	if err != nil {
		return "", err
	}

	value := strings.TrimSpace(string(output))
	if value == "" || value == "(unset)" {
		return "", fmt.Errorf("%s is not set and gcloud returned no value", envKey)
	}
	return value, nil
}

func loadAptPackages(projectPath string) ([]string, error) {
	configPath := aptPackagesPath(projectPath)
	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", configPath, err)
	}
	defer file.Close()

	var packages []string
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		packages = append(packages, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	return packages, nil
}

func loadNodeVersion(projectPath string) (string, error) {
	configPath := nodeVersionPath(projectPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", configPath, err)
	}

	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("%s is empty", configPath)
	}
	if !nodeMajorVersionPattern.MatchString(value) {
		return "", fmt.Errorf("%s must contain only a Node.js major version, got %q", configPath, value)
	}
	return value, nil
}

func loadAgents(projectPath string) ([]string, error) {
	configPath := agentConfigPath(projectPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg agentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if len(cfg.Agents) == 0 {
		return nil, fmt.Errorf("%s does not define any agents", configPath)
	}

	agents := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		switch name {
		case "codex", "claude":
			agents = append(agents, name)
		default:
			return nil, fmt.Errorf("%s contains unsupported agent %q", configPath, name)
		}
	}
	sort.Strings(agents)
	return agents, nil
}

func loadVMConfig(projectPath string) (gcpVMConfig, error) {
	configPath := vmConfigPath(projectPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return gcpVMConfig{
				MachineType:  stringPointer(defaultGCPMachineType),
				BootDiskSize: stringPointer(defaultGCPDiskSize),
				ImageFamily:  stringPointer(defaultGCPImageFamily),
				ImageProject: stringPointer(defaultGCPImageProject),
			}, nil
		}
		return gcpVMConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg vmConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return gcpVMConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if cfg.Provider == nil {
		cfg.Provider = stringPointer("gcp")
	} else if *cfg.Provider == "" {
		return gcpVMConfig{}, fmt.Errorf("%s contains empty provider", configPath)
	}
	if *cfg.Provider != "gcp" {
		return gcpVMConfig{}, fmt.Errorf("%s contains unsupported provider %q", configPath, *cfg.Provider)
	}
	gcp := cfg.GCP
	if gcp.MachineType == nil {
		gcp.MachineType = stringPointer(defaultGCPMachineType)
	} else if *gcp.MachineType == "" {
		return gcpVMConfig{}, fmt.Errorf("%s contains empty gcp.machine_type", configPath)
	}
	if gcp.BootDiskSize == nil {
		gcp.BootDiskSize = stringPointer(defaultGCPDiskSize)
	} else if *gcp.BootDiskSize == "" {
		return gcpVMConfig{}, fmt.Errorf("%s contains empty gcp.boot_disk_size", configPath)
	}
	if gcp.ImageFamily == nil {
		gcp.ImageFamily = stringPointer(defaultGCPImageFamily)
	} else if *gcp.ImageFamily == "" {
		return gcpVMConfig{}, fmt.Errorf("%s contains empty gcp.image_family", configPath)
	}
	if gcp.ImageProject == nil {
		gcp.ImageProject = stringPointer(defaultGCPImageProject)
	} else if *gcp.ImageProject == "" {
		return gcpVMConfig{}, fmt.Errorf("%s contains empty gcp.image_project", configPath)
	}
	return gcp, nil
}

func stringPointer(value string) *string {
	return &value
}

func aptPackagesPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "apt.txt")
}

func nodeVersionPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "node.txt")
}

func agentConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "agent.yaml")
}

func vmConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "vm.yaml")
}
