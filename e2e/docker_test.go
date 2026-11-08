//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// hostDocker runs the Docker of the machine the test runs on, which is the
// host's Docker to isotty, and fails the test when it fails.
func hostDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestDockerCarriesImagesBetweenTheVMAndAPinnedRegistry guards both ways
// of an image the VM cannot move itself. Pulling: the host's Docker pulls
// it from the registry and the VM's `isotty-docker pull` receives it for
// `docker load`, and a second pull moves nothing that both sides kept.
// Pushing: `isotty-docker push` sends what the VM's Docker
// saved, the host's Docker pushes it, the push is in the audit log, a
// second push sends nothing the host still holds, and pulling the pushed
// image back moves nothing. The host's Docker keeps nothing of a pull, and
// of a push only what the host's owner allows in ~/.isotty/docker.yaml; a
// repository that is not pinned for the operation stays out of reach, and
// `isotty down` leaves neither blob nor kept image behind. The VM has no Docker of its own in this test; a stand-in keeps
// what `docker load` is fed and saves an archive made beforehand.
func TestDockerCarriesImagesBetweenTheVMAndAPinnedRegistry(t *testing.T) {
	h := newHarness(t)
	// Where the blobs of a push are kept, so the test can look.
	temporary := filepath.Join(h.root, "tmp")
	if err := os.Mkdir(temporary, 0o700); err != nil {
		t.Fatal(err)
	}
	h.env = append(h.env, "TMPDIR="+temporary)
	blobs := func() []string {
		kept, err := filepath.Glob(filepath.Join(temporary, "isotty-*", "docker", "*", "*"))
		if err != nil {
			t.Fatal(err)
		}
		return kept
	}

	// A registry only the host can reach.
	registry := h.container + "-registry"
	hostDocker(t, "run", "-d", "--rm", "--name", registry, "-p", "127.0.0.1:0:5000", "registry:2")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", registry).Run() })
	address := hostDocker(t, "port", registry, "5000/tcp")
	registryAddress := "127.0.0.1:" + address[strings.LastIndex(address, ":")+1:]
	repo := registryAddress + "/e2e/probe"
	ref := repo + ":1"
	other := registryAddress + "/e2e/other:1"
	builtRepo := registryAddress + "/e2e/built"
	built := builtRepo + ":1"

	// An image that exists in the registry and not in the host's Docker.
	context := filepath.Join(h.root, "image")
	if err := os.Mkdir(context, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"Dockerfile": "FROM scratch\nCOPY probe /probe\n", "probe": h.container + "\n"} {
		if err := os.WriteFile(filepath.Join(context, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{ref, other} {
		hostDocker(t, "build", "-q", "-t", name, context)
		h.waitFor("the registry to take a push", 60*time.Second, func() bool {
			return exec.Command("docker", "push", "-q", name).Run() == nil
		})
		hostDocker(t, "image", "rm", name)
	}
	platform := hostDocker(t, "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}")

	// An image the VM built: it exists as the archive the VM's Docker
	// saves, and neither in the registry nor in the host's Docker.
	if err := os.WriteFile(filepath.Join(context, "probe"), []byte(h.container+" built in the VM\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostDocker(t, "build", "-q", "-t", built, context)
	hostDocker(t, "save", "--output", filepath.Join(h.root, "built.tar"), built)
	hostDocker(t, "image", "rm", built)

	h.isotty("host", "docker", "add", "--repo", repo)
	h.isotty("host", "docker", "add", "--repo", builtRepo, "--allow", "write")
	// The owner of this host lets pushed images stay in its Docker.
	if err := os.WriteFile(filepath.Join(h.home, "docker.yaml"), []byte("keep_pushed: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.isotty("up", "--no-attach")
	hostDocker(t, "cp", filepath.Join(h.root, "built.tar"), h.container+":/tmp/built.tar")
	h.mustVM("sudo chmod 644 /tmp/built.tar")
	// save is asked as `save --platform <platform> --output <file> <ref>`.
	standIn := "#!/bin/sh\ncase \"$1\" in\n  version) echo " + platform + " ;;\n  load) cat > /tmp/loaded.tar ;;\n  save) cat /tmp/built.tar > \"$5\" ;;\n  *) echo \"unexpected docker $*\" >&2; exit 1 ;;\nesac\n"
	h.mustVM("printf '%s' '" + standIn + "' | sudo tee /usr/local/bin/docker >/dev/null && sudo chmod 755 /usr/local/bin/docker")

	s := h.attach()
	h.waitFor("the attach to reach the session", 60*time.Second, func() bool {
		select {
		case err := <-s.done:
			t.Fatalf("the attach ended before reaching the session: %v\n%s", err, s.output.String())
		default:
		}
		out, err := h.vm("tmux list-sessions -F '#{session_name} #{session_attached}'")
		return err == nil && strings.TrimSpace(out) == "isotty 1"
	})

	if out := h.mustVM("isotty-docker status"); !strings.Contains(out, repo+": read") {
		t.Fatalf("status in the VM:\n%s", out)
	}
	if out := h.mustVM("isotty-docker pull " + ref); !strings.Contains(out, "Receiving 2 of 2 blobs") {
		t.Fatalf("pull in the VM:\n%s", out)
	}
	if manifest := h.mustVM("tar -xOf /tmp/loaded.tar manifest.json"); !strings.Contains(manifest, `"`+ref+`"`) {
		t.Fatalf("what the VM was given to load does not name %s:\n%s", ref, manifest)
	}
	if id := hostDocker(t, "image", "ls", "-q", ref); id != "" {
		t.Fatalf("the host's Docker still holds %s as %s after the pull", ref, id)
	}
	if len(blobs()) == 0 {
		t.Fatalf("no blob of the pull is kept under %s for the next one", temporary)
	}
	// The second pull takes the image from what both sides kept; the
	// registry is only asked which image the tag names.
	h.mustVM("rm /tmp/loaded.tar")
	if out := h.mustVM("isotty-docker pull " + ref); !strings.Contains(out, "Receiving 0 of 2 blobs") || strings.Contains(out, "The host pulled from the registry") {
		t.Fatalf("second pull in the VM:\n%s", out)
	}
	if manifest := h.mustVM("tar -xOf /tmp/loaded.tar manifest.json"); !strings.Contains(manifest, `"`+ref+`"`) {
		t.Fatalf("what the VM was given to load the second time does not name %s:\n%s", ref, manifest)
	}

	if out, err := h.vm("isotty-docker pull " + other); err == nil || !strings.Contains(out, "is not pinned") {
		t.Fatalf("pull of a repository that is not pinned: %v\n%s", err, out)
	}

	out := h.mustVM("isotty-docker push " + built)
	pushed := regexp.MustCompile(`Pushed ` + regexp.QuoteMeta(built) + ` as (sha256:[0-9a-f]{64})`).FindStringSubmatch(out)
	if pushed == nil {
		t.Fatalf("push in the VM:\n%s", out)
	}
	req, err := http.NewRequest(http.MethodHead, "http://"+registryAddress+"/v2/e2e/built/manifests/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Docker-Content-Digest") != pushed[1] {
		t.Fatalf("the registry has %s as %d %q, and the VM was told %s", built, resp.StatusCode, resp.Header.Get("Docker-Content-Digest"), pushed[1])
	}
	if id := hostDocker(t, "image", "ls", "-q", built); id == "" {
		t.Fatalf("the host's Docker does not hold %s after the push although keep_pushed is set", built)
	}
	if events := h.mustVM("sudo cat /var/log/isotty/events.jsonl"); !strings.Contains(events, `"type":"docker-push"`) || !strings.Contains(events, `"target":"1@`+pushed[1]+`"`) {
		t.Fatalf("the push is not in the VM event log:\n%s", events)
	}
	if len(blobs()) == 0 {
		t.Fatalf("no blob of the push is kept under %s for the next one", temporary)
	}
	if out := h.mustVM("isotty-docker push " + built); !strings.Contains(out, "Sending 0 of") {
		t.Fatalf("a second push of the same image sent blobs again:\n%s", out)
	}
	// Both sides kept the pushed image, so pulling it back moves nothing.
	h.mustVM("rm /tmp/loaded.tar")
	if out := h.mustVM("isotty-docker pull " + built); !strings.Contains(out, "Receiving 0 of") || strings.Contains(out, "The host pulled from the registry") {
		t.Fatalf("pull of the pushed image:\n%s", out)
	}
	if manifest := h.mustVM("tar -xOf /tmp/loaded.tar manifest.json"); !strings.Contains(manifest, `"`+built+`"`) {
		t.Fatalf("what the VM was given to load for the pushed image does not name %s:\n%s", built, manifest)
	}
	if out, err := h.vm("isotty-docker push " + ref); err == nil || !strings.Contains(out, "pinned for pulling only") {
		t.Fatalf("push to a repository pinned for pulling: %v\n%s", err, out)
	}

	h.mustVM("tmux detach-client -s isotty")
	if err := s.wait(t, 30*time.Second); err != nil {
		t.Fatalf("attach: %v", err)
	}
	h.isotty("down")
	if kept := blobs(); len(kept) != 0 {
		t.Fatalf("`isotty down` left blobs of the push behind: %v", kept)
	}
	if id := hostDocker(t, "image", "ls", "-q", built); id != "" {
		t.Fatalf("`isotty down` left %s in the host's Docker as %s", built, id)
	}
}
