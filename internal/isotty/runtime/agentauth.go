package runtimecfg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var agentHomePaths = map[string][]string{
	"claude": {".claude", ".claude.json"},
	"codex":  {".codex"},
}

// agentHomeExcludes leave out what the agent downloads again on its own:
// caches, and for Codex the app-server binaries it keeps under packages.
var agentHomeExcludes = map[string][]string{
	"claude": {".claude/cache"},
	"codex":  {".codex/.tmp", ".codex/cache", ".codex/plugins/cache", ".codex/packages"},
}

const agentAuthKeyFile = "agents.key"

func AgentAuthArchivePath(projectPath, agent string) string {
	return filepath.Join(projectPath, ".isotty", agent+".tar.gz.enc")
}

// CaptureAgentAuthCommand prints agent's home state as a base64 tar.gz, or
// nothing when none of its paths exist.
func CaptureAgentAuthCommand(agent string) string {
	paths, ok := agentHomePaths[agent]
	if !ok {
		panic(fmt.Sprintf("unsupported agent %q", agent))
	}
	excludes := ""
	for _, exclude := range agentHomeExcludes[agent] {
		excludes += " --exclude=" + shellJoin([]string{exclude})
	}
	return fmt.Sprintf(
		`cd ~ && set -- && for p in %s; do [ -e "$p" ] && set -- "$@" "$p"; done; [ $# -eq 0 ] || tar -czf -%s "$@" | base64 -w0`,
		shellJoin(paths), excludes)
}

// SaveAgentAuth encrypts and stores an archive. An empty archive (the agent
// had no state) removes the file, so the project mirrors the VM at `down`.
func SaveAgentAuth(homeDir, projectPath, agent string, archive []byte) error {
	path := AgentAuthArchivePath(projectPath, agent)
	if len(archive) == 0 {
		return removeIfExists(path)
	}
	key, err := agentAuthKey(homeDir, true)
	if err != nil {
		return err
	}
	sealed, err := seal(key, archive)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create agent login directory: %w", err)
	}
	return os.WriteFile(path, sealed, 0o600)
}

func LoadAgentAuth(homeDir, projectPath, agent string) ([]byte, bool, error) {
	path := AgentAuthArchivePath(projectPath, agent)
	sealed, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	key, err := agentAuthKey(homeDir, false)
	if err != nil {
		return nil, false, err
	}
	archive, err := open(key, sealed)
	if err != nil {
		return nil, false, fmt.Errorf("decrypt %s: %w", path, err)
	}
	return archive, true, nil
}

// RemoveAgentAuth forgets the saved state of agents removed from a project.
func RemoveAgentAuth(projectPath string, agents []string) error {
	for _, agent := range agents {
		if err := removeIfExists(AgentAuthArchivePath(projectPath, agent)); err != nil {
			return err
		}
	}
	return nil
}

func agentAuthKey(homeDir string, create bool) ([]byte, error) {
	path := filepath.Join(homeDir, agentAuthKeyFile)
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("%s must hold 32 bytes, has %d", path, len(key))
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if !create {
		return nil, fmt.Errorf("%s does not exist, so saved agent logins cannot be decrypted; remove them or restore the key", path)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return key, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func seal(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func open(key, sealed []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("sealed data is too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// AgentAuthPaths lists what an agent's archive holds, for documentation and
// progress output.
func AgentAuthPaths(agent string) string {
	return strings.Join(agentHomePaths[agent], ", ")
}
