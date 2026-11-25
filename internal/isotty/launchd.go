package isotty

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// daemonLabel names the launchd agent that keeps `isotty daemon run`
// running on a Mac. legacyLabel is the one the wake runner had before the
// daemon took it over.
const (
	daemonLabel = "isotty.daemon"
	legacyLabel = "isotty.schedule"
)

func launchdAvailable() bool { return runtime.GOOS == "darwin" }

func launchdPlistPath(label string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// launchdPlist runs the daemon at login and again whenever it ends. The
// binary and PATH are the ones of the command that installed it: launchd
// starts agents with a PATH that holds neither gcloud, ssh nor mutagen.
func launchdPlist(binary, logPath string, env map[string]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + daemonLabel + `</string>
  <key>ProgramArguments</key>
  <array><string>` + html.EscapeString(binary) + `</string><string>daemon</string><string>run</string></array>
  <key>EnvironmentVariables</key>
  <dict>
`)
	for _, key := range []string{"PATH", "ISOTTY_HOME"} {
		if value, ok := env[key]; ok {
			b.WriteString("    <key>" + key + "</key><string>" + html.EscapeString(value) + "</string>\n")
		}
	}
	b.WriteString(`  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>` + html.EscapeString(logPath) + `</string>
  <key>StandardErrorPath</key><string>` + html.EscapeString(logPath) + `</string>
</dict>
</plist>
`)
	return b.String()
}

// installLaunchd loads the daemon's agent, writing it first when it is
// missing or names another binary or PATH. An agent already loaded as
// written is left running, so an `up` does not drop every connection the
// daemon holds.
func (a *App) installLaunchd(homeDir string) error {
	if err := removeAgent(legacyLabel); err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	if binary, err = filepath.EvalSymlinks(binary); err != nil {
		return err
	}
	env := map[string]string{"PATH": os.Getenv("PATH")}
	if value, ok := os.LookupEnv("ISOTTY_HOME"); ok {
		env["ISOTTY_HOME"] = value
	}
	path, err := launchdPlistPath(daemonLabel)
	if err != nil {
		return err
	}
	content := []byte(launchdPlist(binary, daemonLogPath(homeDir), env))
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if bytes.Equal(current, content) && launchdLoaded(daemonLabel) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	// launchd would create the log world-readable; it carries prompts.
	logFile, err := os.OpenFile(daemonLogPath(homeDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := logFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(daemonLogPath(homeDir), 0o600); err != nil {
		return err
	}
	if err := bootout(daemonLabel); err != nil {
		return err
	}
	return launchctl("bootstrap", launchdDomain(), path)
}

func (a *App) removeLaunchd() error { return removeAgent(daemonLabel) }

func removeAgent(label string) error {
	if err := bootout(label); err != nil {
		return err
	}
	path, err := launchdPlistPath(label)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// bootout unloads the agent and waits until launchd has let go of it:
// bootout returns while the daemon still stops, which waits for its ssh,
// and a bootstrap before then fails with an I/O error.
func bootout(label string) error {
	if !launchdLoaded(label) {
		return nil
	}
	if err := launchctl("bootout", launchdDomain()+"/"+label); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for launchdLoaded(label) {
		if time.Now().After(deadline) {
			return fmt.Errorf("launchd still holds %s 30 seconds after bootout", label)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func launchdLoaded(label string) bool {
	return exec.Command("launchctl", "print", launchdDomain()+"/"+label).Run() == nil
}

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}
