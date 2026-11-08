// Package docker lets the VM pull images that the host's Docker can pull,
// and push images through it, for the repositories the human pinned. The
// host's own `docker` talks to the registry, so its login, its certificates
// and its reach into a VPN are what count; IsoTTY carries the image from
// one Docker to the other. See docs/DOCKER.md.
package docker

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

type (
	Access        = forge.Access
	ProjectConfig = forge.ProjectConfig
)

// Of the levels a pin can carry, read is pulling and write adds pushing.
const (
	AccessRead  = forge.AccessRead
	AccessWrite = forge.AccessWrite
)

// Kind is Docker as the config files see it: .isotty/docker.yaml lists
// repositories as "registry/path".
var Kind = forge.Kind{Name: "docker", Title: "Docker", ValidateRepo: ValidateRepo}

// A repository is always spelled with its registry. Docker reads a name
// without one as a Docker Hub repository under rules of its own
// (`alpine` is docker.io/library/alpine), and a pin checked against a name
// that Docker then reads differently would be no pin; with the registry
// spelled out, the repository IsoTTY checks is the one Docker uses.
const (
	registryExpr = `[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*(?::[0-9]+)?`
	pathExpr     = `[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*)*`
	tagExpr      = `[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}`
)

var (
	repoPattern = regexp.MustCompile(`^(` + registryExpr + `)/` + pathExpr + `$`)
	refPattern  = regexp.MustCompile(`^((` + registryExpr + `)/` + pathExpr + `):` + tagExpr + `$`)
	// The VM names the platform of its own Docker; the value becomes an
	// argument of the host's.
	platformPattern = regexp.MustCompile(`^linux/[a-z0-9]+(?:/[a-z0-9]+)?$`)
)

// namesRegistry is Docker's own test for whether the first part of a name
// is a registry rather than a Docker Hub namespace.
func namesRegistry(registry string) bool {
	return registry == "localhost" || strings.ContainsAny(registry, ".:")
}

func ValidateRepo(repo string) error {
	match := repoPattern.FindStringSubmatch(repo)
	if match == nil || !namesRegistry(match[1]) {
		return fmt.Errorf("invalid repository %q: expected registry/path, with the registry spelled out as in docker.io/library/alpine", repo)
	}
	return nil
}

// RepoOf returns the repository an image reference names. The reference
// carries its tag: without one Docker would pick `latest` by itself.
func RepoOf(ref string) (string, error) {
	match := refPattern.FindStringSubmatch(ref)
	if match == nil || !namesRegistry(match[2]) {
		return "", fmt.Errorf("invalid image reference %q: expected registry/path:tag, with the registry spelled out as in docker.io/library/alpine:3.20", ref)
	}
	return match[1], nil
}

// CheckRegistry reports whether this machine can open a connection to the
// registry of a repository, on the port its name gives or the HTTPS port.
// It is what can be told of a repository when it is pinned, before any
// image of it is named: a mistyped host, or a network the host is not on.
// It is this machine's reach, not Docker's own attempt, and says nothing of
// the repository or the login; the first pull tells those.
func CheckRegistry(repo string) error {
	match := repoPattern.FindStringSubmatch(repo)
	if match == nil {
		return fmt.Errorf("invalid repository %q", repo)
	}
	address := match[1]
	if !strings.Contains(address, ":") {
		address += ":443"
	}
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return fmt.Errorf("the registry of %s is not reachable from this host: %w; check the name, and that the host is on the network the registry is in", repo, err)
	}
	return conn.Close()
}
