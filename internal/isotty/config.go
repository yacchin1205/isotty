package isotty

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

type Config struct {
	ProjectPath  string
	ProjectHash  string
	GCPProjectID string
	Zone         string
	HomeDir      string
	Runtime      runtimecfg.RuntimeConfig
	VM           vmcfg.VMConfig
}

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

	runtimeConfig, err := runtimecfg.Load(projectPath)
	if err != nil {
		return Config{}, err
	}
	vmConfig, err := vmcfg.Load(projectPath)
	if err != nil {
		return Config{}, err
	}

	return Config{
		ProjectPath:  projectPath,
		ProjectHash:  projectHash,
		GCPProjectID: projectID,
		Zone:         zone,
		HomeDir:      homeDir,
		Runtime:      runtimeConfig,
		VM:           vmConfig,
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
