package docker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runClient runs the VM client the way `isotty up` installs it against a
// gateway serving the relay under /docker, with a `docker` on its PATH
// that reports an amd64 server, keeps what `docker load` is fed and saves
// the image a test put in the VM with vmImage.
func runClient(t *testing.T, engine Engine, args ...string) (output string, loaded string, err error) {
	t.Helper()
	return runClientIn(t, true, engine, args...)
}

func runClientIn(t *testing.T, vmHasDocker bool, engine Engine, args ...string) (output string, loaded string, err error) {
	t.Helper()
	if _, lookErr := exec.LookPath("python3"); lookErr != nil {
		t.Skip("python3 not available")
	}
	mux := http.NewServeMux()
	mux.Handle("/docker/", http.StripPrefix("/docker", Handler(pinned, HostConfig{Keep: DefaultKeep}, engine, CacheFor("abc"), func(PushEvent) error { return nil }, func(err error) { t.Errorf("leftover: %v", err) })))
	gateway := httptest.NewServer(mux)
	t.Cleanup(gateway.Close)

	dir := t.TempDir()
	script := filepath.Join(dir, "isotty-docker")
	port := gateway.URL[strings.LastIndex(gateway.URL, ":")+1:]
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	loadedPath := filepath.Join(dir, "loaded")
	// save is asked as `save --platform <platform> --output <file> <ref>`.
	fakeDocker := "#!/bin/sh\ncase \"$1\" in\n  version) echo linux/amd64 ;;\n  load) cat > " + loadedPath + " ;;\n  save) cp \"$VM_IMAGE\" \"$5\" ;;\n  *) echo \"unexpected docker $*\" >&2; exit 1 ;;\nesac\n"
	path := bin
	if vmHasDocker {
		if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDocker), 0o755); err != nil {
			t.Fatal(err)
		}
		path += string(os.PathListSeparator) + os.Getenv("PATH")
	}
	// The interpreter itself, so that the client's PATH can leave out
	// whatever launcher `python3` is on this machine.
	python, err := exec.Command("python3", "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(strings.TrimSpace(string(python)), append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+path)
	out, err := cmd.CombinedOutput()
	data, readErr := os.ReadFile(loadedPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatal(readErr)
	}
	return string(out), string(data), err
}

// loadedImage reads what the VM's stand-in Docker was fed the way Docker
// would, and returns the image it got under the reference.
func loadedImage(t *testing.T, loaded, ref string) string {
	t.Helper()
	vm := &fakeEngine{refs: map[string]string{}}
	if err := vm.Load(context.Background(), strings.NewReader(loaded)); err != nil {
		t.Fatalf("docker load was fed something it cannot read: %v", err)
	}
	return vm.refs[ref]
}

// The VM's `isotty-docker pull` ends with the image in the VM's Docker,
// asked for the platform of that Docker. The next pull fetches only what
// the VM did not receive before.
func TestClientPullLoadsTheImageIntoTheVMsDocker(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	big := strings.Repeat("image bytes ", 200000)
	engine := &fakeEngine{refs: map[string]string{}, registry: map[string]string{pinnedRef: "config+" + big}}
	out, loaded, err := runClient(t, engine, "pull", pinnedRef)
	if err != nil || !strings.Contains(out, "Receiving 2 of 2 blobs") {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if loadedImage(t, loaded, pinnedRef) != "config+"+big {
		t.Fatalf("docker load was fed %d bytes that are not the image under %s", len(loaded), pinnedRef)
	}
	if engine.calls[1] != "pull linux/amd64 "+pinnedRef {
		t.Fatalf("the host pulled with %q, want the VM's platform", engine.calls[1])
	}

	engine.registry[pinnedRef] = "config two+" + big + "+layer two"
	out, loaded, err = runClient(t, engine, "pull", pinnedRef)
	if err != nil || !strings.Contains(out, "Receiving 2 of 3 blobs") {
		t.Fatalf("pull of the moved tag: %v\n%s", err, out)
	}
	if loadedImage(t, loaded, pinnedRef) != "config two+"+big+"+layer two" {
		t.Fatalf("docker load was fed %d bytes that are not the moved tag's image", len(loaded))
	}
}

// A refusal and a pull the host could not finish both end the command with
// the reason, and neither passes for a loaded image.
func TestClientPullFailsWithTheReason(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	engine := &fakeEngine{refs: map[string]string{}, registry: map[string]string{}}
	out, _, err := runClient(t, engine, "pull", "registry.example.org/team/other:1")
	if err == nil || !strings.Contains(out, "is not pinned") || !strings.Contains(out, "isotty host docker add --repo registry.example.org/team/other") {
		t.Fatalf("pull of an unpinned repository: %v\n%s", err, out)
	}
	out, loaded, err := runClient(t, engine, "pull", pinnedRef)
	if err == nil || !strings.Contains(out, "manifest unknown") || loaded != "" {
		t.Fatalf("pull the host cannot do: %v\n%s", err, out)
	}
	broken := &fakeEngine{refs: map[string]string{}, registry: map[string]string{pinnedRef: "config+" + strings.Repeat("half ", 200000)}, saveErr: errors.New("broke off")}
	out, loaded, err = runClient(t, broken, "pull", pinnedRef)
	if err == nil || !strings.Contains(out, "broke off") || loaded != "" {
		t.Fatalf("pull that breaks off on the host: %v\n%s", err, out)
	}
}

func TestClientStatusListsThePinnedRepositories(t *testing.T) {
	out, _, err := runClient(t, &fakeEngine{}, "status")
	if err != nil || !strings.Contains(out, "registry.example.org/team/base: read") {
		t.Fatalf("status: %v\n%s", err, out)
	}
}

// Pinning a repository does not put Docker into the VM, so a pull there
// says what is missing and how the human gets it, before asking the host
// for anything.
func TestClientPullSaysWhenTheVMHasNoDocker(t *testing.T) {
	engine := &fakeEngine{refs: map[string]string{}, registry: map[string]string{pinnedRef: "config+layer"}}
	out, _, err := runClientIn(t, false, engine, "pull", pinnedRef)
	if err == nil || !strings.Contains(out, "isotty runtime service enable docker") {
		t.Fatalf("pull in a VM without docker: %v\n%s", err, out)
	}
	if len(engine.calls) != 0 {
		t.Fatalf("the host's Docker was used although the VM cannot load: %v", engine.calls)
	}
}

// vmImage puts an image into the VM's stand-in Docker: the archive its
// `docker save` writes, with a name of its own choosing in the manifest.
func vmImage(t *testing.T, config string, layers ...string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(path, archiveOf(strings.Join(append([]string{config}, layers...), "+"), "registry.example.org/team/base:stolen"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VM_IMAGE", path)
}

// The VM's `isotty-docker push` gets an image of the VM's Docker into the
// registry under the reference it was asked for, whatever the VM's own
// archive calls the image, and says which digest the registry has it as.
// Pulling it back costs nothing, and the next push sends only what changed.
func TestClientPushSendsTheVMsImageToTheRegistry(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	engine := &fakeEngine{refs: map[string]string{}}
	vmImage(t, "config", "base layer", strings.Repeat("layer one ", 200000))
	out, _, err := runClient(t, engine, "push", pushRef)
	if err != nil || !strings.Contains(out, "Pushed "+pushRef+" as "+registryDigest) || !strings.Contains(out, "Sending 3 of 3 blobs") {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if engine.pushed[pushRef] != "config+base layer+"+strings.Repeat("layer one ", 200000) {
		t.Fatalf("the registry got %d bytes under %s", len(engine.pushed[pushRef]), pushRef)
	}
	if len(engine.pushed) != 1 || len(engine.refs) != 0 {
		t.Fatalf("pushed %d references, the host's Docker holds %v", len(engine.pushed), engine.refs)
	}
	// Pulling the pushed image back moves nothing: both sides kept it.
	out, loaded, err := runClient(t, engine, "pull", pushRef)
	if err != nil || !strings.Contains(out, "Receiving 0 of 3 blobs") || strings.Contains(out, "The host pulled") {
		t.Fatalf("pull of the pushed image: %v\n%s", err, out)
	}
	if loadedImage(t, loaded, pushRef) != "config+base layer+"+strings.Repeat("layer one ", 200000) {
		t.Fatalf("docker load was fed %d bytes that are not the pushed image", len(loaded))
	}

	vmImage(t, "config two", "base layer", "layer two")
	out, _, err = runClient(t, engine, "push", pushRef)
	if err != nil || !strings.Contains(out, "Sending 2 of 3 blobs") {
		t.Fatalf("second push: %v\n%s", err, out)
	}
	if engine.pushed[pushRef] != "config two+base layer+layer two" {
		t.Fatalf("the registry got %q after the second push", engine.pushed[pushRef])
	}
}

// A push that is refused or that the registry turns down ends the command
// with the reason.
func TestClientPushFailsWithTheReason(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	engine := &fakeEngine{refs: map[string]string{}}
	vmImage(t, "config", "layer")
	out, _, err := runClient(t, engine, "push", pinnedRef)
	if err == nil || !strings.Contains(out, "pinned for pulling only") || !strings.Contains(out, "--allow write") {
		t.Fatalf("push to a repository pinned for pulling: %v\n%s", err, out)
	}
	if len(engine.calls) != 0 {
		t.Fatalf("the host's Docker was used: %v", engine.calls)
	}
	denied := &fakeEngine{refs: map[string]string{}, pushErr: errors.New("denied: requested access to the resource is denied")}
	out, _, err = runClient(t, denied, "push", pushRef)
	if err == nil || !strings.Contains(out, "denied") || strings.Contains(out, "Pushed") {
		t.Fatalf("push the registry turns down: %v\n%s", err, out)
	}
}
