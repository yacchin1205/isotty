package host

import (
	"bufio"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/yazawa/isotty/internal/isotty/host/sshhost"
)

// LoadSSHProjectConfig returns the synced project config once it has been
// checked against the pin this host recorded at `isotty host ssh add`, or
// nil when the project names no machine.
func LoadSSHProjectConfig(projectPath, projectHash, homeDir string) (*sshhost.ProjectConfig, error) {
	synced, enabled, err := sshhost.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := sshhost.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runSSH(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host ssh requires a subcommand: add, remove, or show")
	}
	switch args[0] {
	case "add":
		return runSSHAdd(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "remove":
		return runSSHRemove(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runSSHShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host ssh subcommand %q", args[0])
	}
}

// parseNamed parses flags that follow a machine's name.
func parseNamed(fs *flag.FlagSet, args []string, usage string) (string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", errors.New(usage)
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", errors.New(usage)
	}
	return name, sshhost.ValidateName(name)
}

// The alias under which the host's ssh records and checks the machine's
// host key in the known-hosts file of one login, whatever its address.
const loginAlias = "isotty-host"

// login runs command on the machine with the host's own ssh and the
// human's own credentials, against a known-hosts file of its own: empty,
// so that ssh shows the host key and asks, or holding the recorded keys,
// so that ssh accepts those only. It returns the host keys the file holds
// afterwards.
func login(h sshhost.Host, hostKeys []string, command string, input string, stdout, stderr io.Writer) ([]string, error) {
	dir, err := os.MkdirTemp("", "isotty-ssh-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	knownHosts := filepath.Join(dir, "known_hosts")
	var lines strings.Builder
	for _, key := range hostKeys {
		fmt.Fprintf(&lines, "%s %s\n", loginAlias, key)
	}
	if err := os.WriteFile(knownHosts, []byte(lines.String()), 0o600); err != nil {
		return nil, err
	}
	options := []string{"-o", "StrictHostKeyChecking=ask"}
	if len(hostKeys) > 0 {
		recorded := sshhost.Host{HostKeys: hostKeys}
		options = []string{"-o", "StrictHostKeyChecking=yes", "-o", "HostKeyAlgorithms=" + recorded.HostKeyAlgorithms()}
	}
	args := append(options,
		"-o", "UserKnownHostsFile="+knownHosts,
		"-o", "GlobalKnownHostsFile=none",
		"-o", "HostKeyAlias="+loginAlias,
		"-o", "HashKnownHosts=no",
		"-o", "UpdateHostKeys=no",
		"-p", strconv.Itoa(h.Port),
		"-l", h.User,
		"--", h.Host, command)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ssh %s: %w", h.Address(), err)
	}
	data, err := os.ReadFile(knownHosts)
	if err != nil {
		return nil, err
	}
	var keys []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[0] == loginAlias {
			keys = append(keys, fields[1]+" "+fields[2])
		}
	}
	return keys, nil
}

// appendKey adds the key on its own line, even to a file whose last line
// has no newline.
const appendKey = `umask 077 && mkdir -p ~/.ssh && f=~/.ssh/authorized_keys && { [ ! -s "$f" ] || [ -z "$(tail -c 1 "$f")" ] || echo >> "$f"; } && cat >> "$f"`

// runSSHAdd logs in to the machine once with the human's own credentials:
// ssh shows its host key and asks, and in the same login the project's new
// key for that machine goes into the account's authorized_keys. What was
// accepted is pinned, and the private key is kept on the host.
func runSSHAdd(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host ssh add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	address := fs.String("host", "", "host name or address of the machine, as the host reaches it")
	user := fs.String("user", "", "account on the machine")
	port := fs.Int("port", 22, "SSH port of the machine")
	keyType := fs.String("key-type", "ed25519", "type of the project's key for the machine: ed25519, or ecdsa for an OpenSSH older than 6.5")
	name, err := parseNamed(fs, args, "host ssh add requires a name: isotty host ssh add <name> --host <host> --user <user> [--port <port>]")
	if err != nil {
		return err
	}
	if *address == "" || *user == "" {
		return errors.New("host ssh add requires --host and --user")
	}
	cfg, err := sshhost.LoadPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	if _, ok := cfg.Hosts[name]; ok {
		return fmt.Errorf("%s is already added; `isotty host ssh remove %s` first", name, name)
	}
	h := sshhost.Host{Host: *address, Port: *port, User: *user}
	if err := h.ValidateAddress(); err != nil {
		return err
	}
	public, private, err := newKeyPair(*keyType, "isotty "+projectHash+" "+name)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Logging in to %s with your own credentials, to record its host key and add the project's key for it.\n", h.Address())
	hostKeys, err := login(h, nil, appendKey, public+"\n", stdout, stderr)
	if err != nil {
		return err
	}
	if len(hostKeys) == 0 {
		return fmt.Errorf("ssh recorded no host key for %s", h.Address())
	}
	h.HostKeys = hostKeys
	keyPath := sshhost.KeyPath(homeDir, projectHash, name)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, []byte(private), 0o600); err != nil {
		return err
	}
	cfg.Hosts[name] = h
	if err := sshhost.Save(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Added %s: %s\n", name, h.Address())
	for _, key := range h.HostKeys {
		fmt.Fprintf(stdout, "  host key %s\n", fingerprint(key))
	}
	fmt.Fprintf(stdout, "Run `isotty up` to set it up in the VM; there, `ssh %s` reaches it while `isotty attach` runs.\n", name)
	return nil
}

// runSSHRemove takes the project's key out of the machine's authorized_keys
// under the recorded host key, then forgets the machine. --force forgets it
// without logging in, for a machine that is gone.
func runSSHRemove(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host ssh remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "forget the machine without taking the key out of its authorized_keys")
	name, err := parseNamed(fs, args, "host ssh remove requires a name: isotty host ssh remove <name> [--force]")
	if err != nil {
		return err
	}
	cfg, err := sshhost.LoadPin(homeDir, projectHash)
	if err != nil {
		return err
	}
	h, ok := cfg.Hosts[name]
	if !ok {
		return fmt.Errorf("%s is not a machine of this project", name)
	}
	keyPath := sshhost.KeyPath(homeDir, projectHash, name)
	if !*force {
		data, err := os.ReadFile(keyPath)
		if err != nil {
			return fmt.Errorf("read the key of %s: %w; `--force` forgets the machine without taking the key out", name, err)
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return fmt.Errorf("parse the key of %s: %w", name, err)
		}
		blob := strings.Fields(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))[1]
		fmt.Fprintf(stdout, "Logging in to %s with your own credentials, to take the project's key out of its authorized_keys.\n", h.Address())
		command := `f=~/.ssh/authorized_keys && if [ -f "$f" ]; then t=$(mktemp ~/.ssh/.authorized_keys.XXXXXX) && { grep -vF -- ` + blob + ` "$f" > "$t" || [ $? -eq 1 ]; } && mv "$t" "$f"; fi`
		if _, err := login(h, h.HostKeys, command, "", stdout, stderr); err != nil {
			return fmt.Errorf("%w; `isotty host ssh remove %s --force` forgets a machine that is gone, leaving the key in its authorized_keys", err, name)
		}
	}
	delete(cfg.Hosts, name)
	if err := sshhost.Save(projectPath, projectHash, homeDir, cfg); err != nil {
		return err
	}
	if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if *force {
		fmt.Fprintf(stdout, "Forgot %s; its authorized_keys still holds the project's key.\n", name)
	} else {
		fmt.Fprintf(stdout, "Removed %s and the project's key from it.\n", name)
	}
	fmt.Fprintln(stdout, "The next `isotty up` takes its key out of the VM.")
	return nil
}

func runSSHShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host ssh show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host ssh show does not accept arguments")
	}
	cfg, err := LoadSSHProjectConfig(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "No machine is added for this project.")
		return nil
	}
	fmt.Fprintln(stdout, "hosts:")
	for _, name := range cfg.Names() {
		h := cfg.Hosts[name]
		fmt.Fprintf(stdout, "  %s: %s\n", name, h.Address())
		for _, key := range h.HostKeys {
			fmt.Fprintf(stdout, "    host key %s\n", fingerprint(key))
		}
	}
	fmt.Fprintf(stdout, "keys: %s\n", sshhost.KeyDir(homeDir, projectHash))
	return nil
}

func fingerprint(key string) string {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
	if err != nil {
		return key
	}
	return parsed.Type() + " " + ssh.FingerprintSHA256(parsed)
}

// newKeyPair returns an authorized_keys line and the matching OpenSSH
// private key. ed25519 is the default; ECDSA is for an OpenSSH older than
// 6.5, which has no ed25519 but takes ECDSA from 5.7 on without the SHA-1
// signatures an RSA key would need there.
func newKeyPair(keyType, comment string) (string, string, error) {
	var public, private crypto.PublicKey
	switch keyType {
	case "ed25519":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", "", err
		}
		public, private = pub, priv
	case "ecdsa":
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return "", "", err
		}
		public, private = &priv.PublicKey, priv
	default:
		return "", "", fmt.Errorf("--key-type %q must be ed25519 or ecdsa", keyType)
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(private, comment)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))) + " " + comment, string(pem.EncodeToMemory(block)), nil
}
