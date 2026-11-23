package sshhost

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

//go:embed assets/isotty-ssh.py
var clientScript string

//go:embed assets/SKILL.md
var skill string

const (
	ClientPath = "/usr/local/bin/isotty-ssh"
	SkillName  = "isotty-ssh"
	// ConfigPath is where the VM's ssh finds the machines: Debian and
	// Ubuntu include ssh_config.d at the top of their ssh_config.
	ConfigPath = "/etc/ssh/ssh_config.d/isotty-hosts.conf"
	keyDir     = ".ssh/isotty-hosts"
)

// SetupCommand installs the VM-side client, the sshd keepalive, the skill
// for each configured agent and, from the archive SetupInput makes on its
// standard input, the keys and the ssh config of the machines. It runs on
// every `isotty up` and replaces what an earlier one wrote, so a machine
// removed from the project loses its key in the VM too.
func SetupCommand(agents []string) string {
	parts := []string{
		"set -euo pipefail",
		gateway.WriteFileCommand(ClientPath, render(clientScript), true),
		"sudo chmod 755 " + ClientPath,
	}
	parts = append(parts, gateway.KeepaliveCommands()...)
	parts = append(parts, gateway.SkillCommands(runtimecfg.AgentSkillDirs(agents), SkillName, render(skill))...)
	parts = append(parts,
		`tmp=$(mktemp -d)`,
		`tar -xzf - -C "$tmp"`,
		`install -d -m 700 ~/.ssh`,
		`rm -rf ~/`+keyDir,
		`mv "$tmp/isotty-hosts" ~/`+keyDir,
		`sudo install -m 644 "$tmp/isotty-hosts.conf" `+ConfigPath,
		`rm -rf "$tmp"`,
	)
	return strings.Join(parts, " && ")
}

func render(template string) string {
	return strings.ReplaceAll(template, "__ISOTTY_GATEWAY_PORT__", strconv.Itoa(gateway.VMPort))
}

// SetupInput is the archive SetupCommand reads: each machine's private key
// and the host keys the human accepted under ~/.ssh/isotty-hosts, and the
// ssh config naming the machines. It goes to the VM on standard input,
// since a command line is what the VM's audit log records.
func SetupInput(cfg ProjectConfig, homeDir, projectHash string) ([]byte, error) {
	var conf, knownHosts strings.Builder
	conf.WriteString("# Written by `isotty up` from .isotty/ssh.yaml; the next `up` replaces it.\n")
	files := map[string][]byte{}
	var order []string
	for _, name := range cfg.Names() {
		h := cfg.Hosts[name]
		key, err := os.ReadFile(KeyPath(homeDir, projectHash, name))
		if err != nil {
			return nil, fmt.Errorf("read the key of %s: %w; run `isotty host ssh add %s` again", name, err, name)
		}
		path := "isotty-hosts/" + name
		files[path] = key
		order = append(order, path)
		alias := "isotty-host-" + name
		for _, hostKey := range h.HostKeys {
			fmt.Fprintf(&knownHosts, "%s %s\n", alias, hostKey)
		}
		fmt.Fprintf(&conf, "\nHost %s\n  HostName %s\n  Port %d\n  User %s\n  IdentityFile ~/%s/%s\n  IdentitiesOnly yes\n  ProxyCommand %s connect %s\n  HostKeyAlias %s\n  HostKeyAlgorithms %s\n  UserKnownHostsFile ~/%s/known_hosts\n  StrictHostKeyChecking yes\n  UpdateHostKeys no\n",
			name, h.Host, h.Port, h.User, keyDir, name, ClientPath, name, alias, h.HostKeyAlgorithms(), keyDir)
	}
	files["isotty-hosts/known_hosts"] = []byte(knownHosts.String())
	files["isotty-hosts.conf"] = []byte(conf.String())
	order = append(order, "isotty-hosts/known_hosts", "isotty-hosts.conf")

	var out bytes.Buffer
	zipped := gzip.NewWriter(&out)
	archive := tar.NewWriter(zipped)
	now := time.Now()
	if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "isotty-hosts/", Mode: 0o700, ModTime: now}); err != nil {
		return nil, err
	}
	for _, path := range order {
		if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: path, Mode: 0o600, Size: int64(len(files[path])), ModTime: now}); err != nil {
			return nil, err
		}
		if _, err := archive.Write(files[path]); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := zipped.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
