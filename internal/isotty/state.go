package isotty

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type State struct {
	ProjectPath          string   `json:"project_path"`
	ProjectHash          string   `json:"project_hash"`
	Backend              string   `json:"backend"`
	GCPProjectID         string   `json:"gcp_project_id"`
	Zone                 string   `json:"zone"`
	InstanceName         string   `json:"instance_name"`
	SessionName          string   `json:"session_name"`
	SyncMode             string   `json:"sync_mode"`
	RemoteWorkspacePath  string   `json:"remote_workspace_path"`
	MutagenDataDirectory string   `json:"mutagen_data_directory"`
	SSHConfigPath        string   `json:"ssh_config_path"`
	SSHWrapperDir        string   `json:"ssh_wrapper_dir"`
	AptPackages          []string `json:"apt_packages,omitempty"`
	NodeVersion          string   `json:"node_version,omitempty"`
	Agents               []string `json:"agents,omitempty"`
	CreatedAt            string   `json:"created_at"`
}

func NewState(cfg Config, syncMode string) State {
	return State{
		ProjectPath:          cfg.ProjectPath,
		ProjectHash:          cfg.ProjectHash,
		Backend:              "gcp-vm",
		GCPProjectID:         cfg.GCPProjectID,
		Zone:                 cfg.Zone,
		InstanceName:         "isotty-" + cfg.ProjectHash,
		SessionName:          "isotty-" + cfg.ProjectHash,
		SyncMode:             syncMode,
		RemoteWorkspacePath:  defaultWorkspacePath,
		MutagenDataDirectory: filepath.Join(cfg.HomeDir, "mutagen"),
		SSHConfigPath:        filepath.Join(cfg.HomeDir, "ssh", "config"),
		SSHWrapperDir:        filepath.Join(cfg.HomeDir, "ssh", "bin"),
		AptPackages:          append([]string(nil), cfg.AptPackages...),
		NodeVersion:          cfg.NodeVersion,
		Agents:               append([]string(nil), cfg.Agents...),
		CreatedAt:            time.Now().UTC().Format(time.RFC3339),
	}
}

func (s State) RemoteEndpoint() string {
	return fmt.Sprintf("%s.%s.%s:%s", s.InstanceName, s.Zone, s.GCPProjectID, s.RemoteWorkspacePath)
}

func (s State) MutagenEnv() []string {
	env := os.Environ()
	env = append(env, "MUTAGEN_DATA_DIRECTORY="+s.MutagenDataDirectory)
	env = append(env, "MUTAGEN_SSH_PATH="+s.SSHWrapperDir)
	return env
}

func (s State) MutagenLabels() []string {
	return []string{
		"app=isotty",
		"project_hash=" + s.ProjectHash,
		"backend=" + s.Backend,
		"sync_mode=" + s.SyncMode,
	}
}

func (s State) MutagenLabelSelector() string {
	return strings.Join(s.MutagenLabels(), ",")
}

func SaveState(state State) error {
	path, err := stateFilePath(state.ProjectHash)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func LoadStateForProject(projectPath string) (State, error) {
	hash := hashProjectPath(projectPath)
	path, err := stateFilePath(hash)
	if err != nil {
		return State{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, fmt.Errorf("no IsoTTY environment found for %s", projectPath)
		}
		return State{}, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func DeleteState(projectHash string) error {
	path, err := stateFilePath(projectHash)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func stateFilePath(projectHash string) (string, error) {
	homeDir, err := isottyHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, "projects", projectHash+".json"), nil
}
