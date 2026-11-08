package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// Engine is the host's Docker as the relay uses it.
type Engine interface {
	// ImageID returns the image a reference names locally, or "" when it
	// names none.
	ImageID(ctx context.Context, ref string) (string, error)
	// Config returns the digest of the config of the image the registry
	// serves for the reference on the platform, without pulling it.
	Config(ctx context.Context, ref, platform string) (string, error)
	Pull(ctx context.Context, ref, platform string) error
	// Save writes the image as `docker load` reads it.
	Save(ctx context.Context, ref, platform string, w io.Writer) error
	// Load takes in an archive of the kind `docker save` writes, under the
	// references the archive names.
	Load(ctx context.Context, archive io.Reader) error
	// Push sends the image to its registry and returns the digest the
	// registry has it under.
	Push(ctx context.Context, ref string) (string, error)
	Tag(ctx context.Context, image, ref string) error
	// Named reports whether any tag names the image.
	Named(ctx context.Context, image string) (bool, error)
	// Remove takes a reference away, and the image with its last one.
	Remove(ctx context.Context, refOrImage string) error
}

// CLI runs the `docker` command of the host, so everything about reaching
// a registry is as the human set their Docker up.
type CLI struct{}

func (c CLI) run(ctx context.Context, stdout io.Writer, args ...string) error {
	return c.feed(ctx, nil, stdout, args...)
}

func (CLI) feed(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (c CLI) ImageID(ctx context.Context, ref string) (string, error) {
	var out bytes.Buffer
	if err := c.run(ctx, &out, "image", "ls", "--no-trunc", "--format", "{{.ID}}", ref); err != nil {
		return "", err
	}
	// One line per platform the image store holds under the reference.
	id, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\n")
	return id, nil
}

// manifest is what the registry serves for a reference: either one image
// or an index listing an image for each platform.
type manifest struct {
	Config    struct{ Digest string }
	Manifests []struct {
		Digest   string
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		}
	}
}

// Config asks the registry through `docker buildx imagetools inspect`,
// which fetches the one manifest it is asked for. `docker manifest inspect`
// would fetch the image of every platform behind a tag, a dozen round trips
// for a Docker Hub image, and it does not read the daemon's insecure
// registry settings.
func (c CLI) Config(ctx context.Context, ref, platform string) (string, error) {
	inspect := func(ref string) (manifest, error) {
		var out bytes.Buffer
		if err := c.run(ctx, &out, "buildx", "imagetools", "inspect", "--raw", ref); err != nil {
			return manifest{}, err
		}
		var m manifest
		if err := json.Unmarshal(out.Bytes(), &m); err != nil {
			return manifest{}, fmt.Errorf("docker buildx imagetools inspect %s: %w", ref, err)
		}
		return m, nil
	}
	m, err := inspect(ref)
	if err != nil {
		return "", err
	}
	// An index is followed to the image for the platform, by digest.
	for _, entry := range m.Manifests {
		if entry.Platform.OS+"/"+entry.Platform.Architecture == platform {
			repo, _ := RepoOf(ref)
			if m, err = inspect(repo + "@" + entry.Digest); err != nil {
				return "", err
			}
			break
		}
	}
	if m.Config.Digest == "" {
		return "", fmt.Errorf("the registry serves no image of %s for %s", ref, platform)
	}
	return m.Config.Digest, nil
}

func (c CLI) Pull(ctx context.Context, ref, platform string) error {
	return c.run(ctx, io.Discard, "pull", "--quiet", "--platform", platform, ref)
}

func (c CLI) Save(ctx context.Context, ref, platform string, w io.Writer) error {
	return c.run(ctx, w, "save", "--platform", platform, ref)
}

func (c CLI) Load(ctx context.Context, archive io.Reader) error {
	return c.feed(ctx, archive, io.Discard, "load", "--quiet")
}

// Docker ends what it prints for a push with "<tag>: digest: sha256:...
// size: <n>"; no other command tells the digest of what was just pushed
// without rules of its own for how the repository is spelled.
var pushedDigest = regexp.MustCompile(`digest: (sha256:[0-9a-f]{64})`)

func (c CLI) Push(ctx context.Context, ref string) (string, error) {
	var out bytes.Buffer
	if err := c.run(ctx, &out, "push", ref); err != nil {
		return "", err
	}
	match := pushedDigest.FindStringSubmatch(out.String())
	if match == nil {
		return "", fmt.Errorf("docker push %s ended without naming a digest: %s", ref, strings.TrimSpace(out.String()))
	}
	return match[1], nil
}

func (c CLI) Tag(ctx context.Context, image, ref string) error {
	return c.run(ctx, io.Discard, "tag", image, ref)
}

func (c CLI) Named(ctx context.Context, image string) (bool, error) {
	var out bytes.Buffer
	if err := c.run(ctx, &out, "image", "inspect", "--format", "{{json .RepoTags}}", image); err != nil {
		return false, err
	}
	var tags []string
	if err := json.Unmarshal(out.Bytes(), &tags); err != nil {
		return false, fmt.Errorf("docker image inspect %s: tags %q: %w", image, strings.TrimSpace(out.String()), err)
	}
	return len(tags) > 0, nil
}

func (c CLI) Remove(ctx context.Context, refOrImage string) error {
	return c.run(ctx, io.Discard, "image", "rm", refOrImage)
}
