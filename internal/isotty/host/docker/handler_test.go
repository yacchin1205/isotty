package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeEngine is a Docker that holds references to images and can be told
// to fail; calls are what it was asked to do. An image is spelled as its
// config and its layers joined with "+", so that a test sees what arrived
// where.
type fakeEngine struct {
	refs      map[string]string // reference -> image
	registry  map[string]string // reference -> image a pull brings
	configErr error             // what asking the registry about a tag fails with
	saveErr   error
	pushed    map[string]string // reference -> image a push sent
	pushErr   error
	calls     []string
}

func (f *fakeEngine) Config(_ context.Context, ref, platform string) (string, error) {
	f.calls = append(f.calls, "inspect "+ref)
	if f.configErr != nil {
		return "", f.configErr
	}
	image, ok := f.registry[ref]
	if !ok {
		return "", errors.New("no such manifest")
	}
	return digestOf(strings.Split(image, "+")[0]), nil
}

// Load takes the archive the way Docker does: the image is its config, and
// it gets the references the manifest names. The image is spelled with its
// layers so that a test sees what arrived.
func (f *fakeEngine) Load(_ context.Context, archive io.Reader) error {
	files := map[string]string{}
	reader := tar.NewReader(archive)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		files[header.Name] = string(data)
	}
	var images []struct {
		Config   string
		RepoTags []string
		Layers   []string
	}
	if err := json.Unmarshal([]byte(files["manifest.json"]), &images); err != nil {
		return err
	}
	for _, image := range images {
		id := files[image.Config]
		for _, layer := range image.Layers {
			id += "+" + files[layer]
		}
		for _, ref := range image.RepoTags {
			f.calls = append(f.calls, "load "+ref)
			f.refs[ref] = id
		}
	}
	return nil
}

func (f *fakeEngine) Push(_ context.Context, ref string) (string, error) {
	f.calls = append(f.calls, "push "+ref)
	if f.pushErr != nil {
		return "", f.pushErr
	}
	if f.pushed == nil {
		f.pushed = map[string]string{}
	}
	if f.registry == nil {
		f.registry = map[string]string{}
	}
	f.pushed[ref] = f.refs[ref]
	f.registry[ref] = f.refs[ref]
	return registryDigest, nil
}

func (f *fakeEngine) ImageID(_ context.Context, ref string) (string, error) {
	return f.refs[ref], nil
}

func (f *fakeEngine) Pull(_ context.Context, ref, platform string) error {
	f.calls = append(f.calls, "pull "+platform+" "+ref)
	image, ok := f.registry[ref]
	if !ok {
		return errors.New("manifest unknown")
	}
	f.refs[ref] = image
	return nil
}

// Save writes the archive Docker would: the config and the layers as blobs
// named by their digests, and a manifest.json naming them. Told to fail, it
// breaks off half way.
func (f *fakeEngine) Save(_ context.Context, ref, platform string, w io.Writer) error {
	f.calls = append(f.calls, "save "+platform+" "+ref)
	archive := archiveOf(f.refs[ref], ref)
	if f.saveErr != nil {
		archive = archive[:len(archive)/2]
	}
	if _, err := w.Write(archive); err != nil {
		return err
	}
	return f.saveErr
}

// archiveOf writes an image as `docker save` does, under the reference.
func archiveOf(image, ref string) []byte {
	name := func(content string) string {
		return "blobs/sha256/" + strings.TrimPrefix(digestOf(content), "sha256:")
	}
	contents := strings.Split(image, "+")
	entry := map[string]any{"Config": name(contents[0]), "RepoTags": []string{ref}, "Layers": []string{}}
	for _, layer := range contents[1:] {
		entry["Layers"] = append(entry["Layers"].([]string), name(layer))
	}
	manifest, err := json.Marshal([]any{entry})
	if err != nil {
		panic(err)
	}
	var out bytes.Buffer
	archive := tar.NewWriter(&out)
	add := func(name string, content []byte) {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			panic(err)
		}
		if _, err := archive.Write(content); err != nil {
			panic(err)
		}
	}
	for _, content := range contents {
		add(name(content), []byte(content))
	}
	add("manifest.json", manifest)
	if err := archive.Close(); err != nil {
		panic(err)
	}
	return out.Bytes()
}

func (f *fakeEngine) Tag(_ context.Context, image, ref string) error {
	f.calls = append(f.calls, "tag "+image+" "+ref)
	f.refs[ref] = image
	return nil
}

func (f *fakeEngine) Named(_ context.Context, image string) (bool, error) {
	for _, named := range f.refs {
		if named == image {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeEngine) Remove(_ context.Context, refOrImage string) error {
	f.calls = append(f.calls, "rm "+refOrImage)
	delete(f.refs, refOrImage)
	return nil
}

const (
	pinnedRef = "registry.example.org/team/base:1.2"
	// pushRef names an image of the repository pinned for pushing.
	pushRef        = "registry.example.org/team/app:2.0"
	registryDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

// pinned is a project with one repository to pull from and one to push to.
var pinned = ProjectConfig{Repos: map[string]Access{
	"registry.example.org/team/base": AccessRead,
	"registry.example.org/team/app":  AccessWrite,
}}

// relayFor serves the relay over a cache of its own; the events are the
// pushes it recorded.
func relayFor(t *testing.T, cfg ProjectConfig, engine Engine) (*httptest.Server, *[]PushEvent) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	events := &[]PushEvent{}
	server := httptest.NewServer(Handler(cfg, HostConfig{Keep: DefaultKeep}, engine, CacheFor("abc"), func(event PushEvent) error {
		*events = append(*events, event)
		return nil
	}, func(err error) { t.Errorf("leftover: %v", err) }))
	t.Cleanup(server.Close)
	return server, events
}

// pullImage does what the VM's client does for a pull: it asks for the
// image and fetches each of its blobs. It returns the image as the fake
// spells it, and how many blobs the host said it had pulled for.
func pullImage(t *testing.T, server *httptest.Server, ref string) (status int, answer string, image string) {
	t.Helper()
	status, answer = askJSON(t, server, "/pull", map[string]string{"ref": ref, "platform": "linux/amd64"})
	if status != http.StatusOK {
		return status, answer, ""
	}
	var listed Image
	if err := json.Unmarshal([]byte(answer), &listed); err != nil {
		t.Fatal(err)
	}
	contents := []string{}
	for _, digest := range append([]string{listed.Config}, listed.Layers...) {
		status, content := ask(t, server, http.MethodGet, "/blobs/"+digest, nil)
		if status != http.StatusOK {
			t.Fatalf("fetching %s = %d %s", digest, status, content)
		}
		if digestOf(content) != digest {
			t.Fatalf("%s arrived as %q", digest, content)
		}
		contents = append(contents, content)
	}
	return status, answer, strings.Join(contents, "+")
}

// pulled tells whether the host's Docker was asked to pull.
func pulled(calls []string) bool {
	for _, call := range calls {
		if strings.HasPrefix(call, "pull ") {
			return true
		}
	}
	return false
}

// A pull hands the VM the image and leaves the host's Docker as it was:
// the host is a way through, and its Docker is not where images collect.
func TestPullLeavesTheHostsDockerAsItWas(t *testing.T) {
	for name, tc := range map[string]struct {
		before map[string]string
		want   map[string]string
	}{
		"the host did not have the image": {
			before: map[string]string{},
			want:   map[string]string{},
		},
		"the host had the same image": {
			before: map[string]string{pinnedRef: "new+layer"},
			want:   map[string]string{pinnedRef: "new+layer"},
		},
		"the host had an older image under the name": {
			before: map[string]string{pinnedRef: "old+layer"},
			want:   map[string]string{pinnedRef: "old+layer"},
		},
		"the host has the pulled image under another name too": {
			before: map[string]string{pinnedRef: "old+layer", "mine:1": "new+layer"},
			want:   map[string]string{pinnedRef: "old+layer", "mine:1": "new+layer"},
		},
	} {
		engine := &fakeEngine{refs: tc.before, registry: map[string]string{pinnedRef: "new+layer"}}
		server, _ := relayFor(t, pinned, engine)
		status, answer, image := pullImage(t, server, pinnedRef)
		if status != http.StatusOK || image != "new+layer" {
			t.Fatalf("%s: pull = %d %s, the image %q", name, status, answer, image)
		}
		if !reflect.DeepEqual(engine.refs, tc.want) {
			t.Fatalf("%s: the host's Docker holds %v afterwards, want %v\ncalls: %v", name, engine.refs, tc.want, engine.calls)
		}
		if len(engine.calls) < 3 || !reflect.DeepEqual(engine.calls[:3], []string{"inspect " + pinnedRef, "pull linux/amd64 " + pinnedRef, "save linux/amd64 " + pinnedRef}) {
			t.Fatalf("%s: calls = %v, want the image pulled and saved for the VM's platform", name, engine.calls)
		}
	}
}

// A pull run again, after a cut or for the same tag, takes the image from
// what the host kept of the last one; the registry is asked only which
// image the tag names now, so a tag that moved is pulled afresh.
func TestPullRunAgainTakesTheImageFromWhatTheHostKept(t *testing.T) {
	engine := &fakeEngine{refs: map[string]string{}, registry: map[string]string{pinnedRef: "config+base layer+layer one"}}
	server, _ := relayFor(t, pinned, engine)
	if status, answer, image := pullImage(t, server, pinnedRef); status != http.StatusOK || image != "config+base layer+layer one" {
		t.Fatalf("first pull = %d %s, the image %q", status, answer, image)
	}
	engine.calls = nil
	status, answer, image := pullImage(t, server, pinnedRef)
	if status != http.StatusOK || image != "config+base layer+layer one" {
		t.Fatalf("second pull = %d %s, the image %q", status, answer, image)
	}
	if !reflect.DeepEqual(engine.calls, []string{"inspect " + pinnedRef}) {
		t.Fatalf("the second pull asked the host's Docker for %v, want only which image the tag names", engine.calls)
	}

	engine.registry[pinnedRef] = "config two+base layer+layer two"
	engine.calls = nil
	status, answer, image = pullImage(t, server, pinnedRef)
	if status != http.StatusOK || image != "config two+base layer+layer two" {
		t.Fatalf("pull of the moved tag = %d %s, the image %q", status, answer, image)
	}
	if !pulled(engine.calls) {
		t.Fatalf("the moved tag was not pulled: %v", engine.calls)
	}

	// A registry the CLI cannot ask leaves the pull to find out.
	engine.configErr = errors.New("http: server gave HTTP response to HTTPS client")
	engine.calls = nil
	status, answer, image = pullImage(t, server, pinnedRef)
	if status != http.StatusOK || image != "config two+base layer+layer two" || !strings.Contains(answer, "HTTPS client") {
		t.Fatalf("pull from a registry that cannot be asked = %d %s, the image %q", status, answer, image)
	}
	if !pulled(engine.calls) {
		t.Fatalf("the tag was not pulled although the registry could not be asked about it: %v", engine.calls)
	}
}

// The host gives only blobs it holds, and says so when one is gone.
func TestPullGivesOnlyWhatTheHostHolds(t *testing.T) {
	server, _ := relayFor(t, pinned, &fakeEngine{})
	if status, answer := ask(t, server, http.MethodGet, "/blobs/"+digestOf("never pulled"), nil); status != http.StatusNotFound || !strings.Contains(answer, "run the pull again") {
		t.Fatalf("a blob the host does not hold = %d %s", status, answer)
	}
	if status, answer := ask(t, server, http.MethodGet, "/blobs/../manifest", nil); status != http.StatusBadRequest && status != http.StatusNotFound {
		t.Fatalf("a path that is not a digest = %d %s", status, answer)
	}
}

// Only a pinned repository is pulled; anything else is refused before the
// host's Docker, and with it the host's login, is used at all.
func TestPullRefusesWhatIsNotPinned(t *testing.T) {
	for _, ref := range []string{
		"registry.example.org/team/other:1",
		"registry.example.org/team/base/sub:1",
		"other.example.org/team/base:1.2",
		"team/base:1.2",
		"registry.example.org/team/base",
	} {
		engine := &fakeEngine{refs: map[string]string{}, registry: map[string]string{ref: "x+y"}}
		server, _ := relayFor(t, pinned, engine)
		status, answer, _ := pullImage(t, server, ref)
		if status != http.StatusForbidden && status != http.StatusBadRequest {
			t.Fatalf("pull of %s = %d %s, want it refused", ref, status, answer)
		}
		if len(engine.calls) != 0 {
			t.Fatalf("pull of %s reached the host's Docker: %v", ref, engine.calls)
		}
	}
}

// What the host's Docker says when it cannot pull is what the VM hears.
func TestPullSaysWhyTheHostCouldNotPull(t *testing.T) {
	engine := &fakeEngine{refs: map[string]string{}, registry: map[string]string{}}
	server, _ := relayFor(t, pinned, engine)
	status, answer, _ := pullImage(t, server, pinnedRef)
	if status != http.StatusBadGateway || !strings.Contains(answer, "manifest unknown") {
		t.Fatalf("pull = %d %s, want the host's error", status, answer)
	}
	if len(engine.refs) != 0 {
		t.Fatalf("the host's Docker holds %v after a failed pull", engine.refs)
	}
}

// An archive that breaks off on the host is not passed for the image, and
// the host's Docker is put back all the same.
func TestPullThatBreaksOffOnTheHostSaysSo(t *testing.T) {
	engine := &fakeEngine{refs: map[string]string{}, registry: map[string]string{pinnedRef: "config+layer"}, saveErr: errors.New("broke off")}
	server, _ := relayFor(t, pinned, engine)
	status, answer, _ := pullImage(t, server, pinnedRef)
	if status != http.StatusBadGateway || !strings.Contains(answer, "broke off") {
		t.Fatalf("pull = %d %s, want the host's error", status, answer)
	}
	if len(engine.refs) != 0 {
		t.Fatalf("the host's Docker holds %v after a broken pull", engine.refs)
	}
}

func TestStatusListsThePinnedRepositories(t *testing.T) {
	cfg := ProjectConfig{Repos: map[string]Access{"registry.example.org/team/base": AccessRead}}
	server, _ := relayFor(t, cfg, &fakeEngine{})
	resp, err := http.Get(server.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status struct{ Repos map[string]string }
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(status.Repos) != "map[registry.example.org/team/base:read]" {
		t.Fatalf("status = %v", status.Repos)
	}
}

func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ask sends a request to the relay and returns the status with the body.
func ask(t *testing.T, server *httptest.Server, method, path string, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func askJSON(t *testing.T, server *httptest.Server, path string, payload any) (int, string) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return ask(t, server, http.MethodPost, path, bytes.NewReader(body))
}

// sendImage does what the VM's client does for a push: it sends the blobs
// the host says it lacks and asks for the push. It returns how many blobs
// it had to send.
func sendImage(t *testing.T, server *httptest.Server, ref, config string, layers ...string) (sent int, status int, answer string) {
	t.Helper()
	contents := map[string]string{}
	digests := []string{}
	for _, content := range append([]string{config}, layers...) {
		contents[digestOf(content)] = content
		digests = append(digests, digestOf(content))
	}
	status, answer = askJSON(t, server, "/blobs/missing", map[string]any{"ref": ref, "digests": digests})
	if status != http.StatusOK {
		return 0, status, answer
	}
	var listed struct{ Missing []string }
	if err := json.Unmarshal([]byte(answer), &listed); err != nil {
		t.Fatal(err)
	}
	for _, digest := range listed.Missing {
		if status, answer := ask(t, server, http.MethodPut, "/blobs/"+digest, strings.NewReader(contents[digest])); status != http.StatusOK {
			t.Fatalf("sending %s = %d %s", digest, status, answer)
		}
	}
	status, answer = askJSON(t, server, "/push", map[string]any{"ref": ref, "config": digests[0], "layers": digests[1:]})
	return len(listed.Missing), status, answer
}

// A push puts the VM's image into the registry under the pinned reference,
// is recorded with the digest the registry gave it, and leaves the host's
// Docker as it was, as a pull does.
func TestPushSendsTheImageAndLeavesTheHostsDockerAsItWas(t *testing.T) {
	for name, before := range map[string]map[string]string{
		"the host did not have the image":             {},
		"the host had another image under the name":   {pushRef: "old"},
		"the host has the image under another name":   {pushRef: "old", "mine:1": "config+layer one+layer two"},
		"the host had the same image under the name":  {pushRef: "config+layer one+layer two"},
		"the host has images that are not this one's": {"mine:1": "other"},
	} {
		want := map[string]string{}
		for ref, image := range before {
			want[ref] = image
		}
		engine := &fakeEngine{refs: before}
		server, events := relayFor(t, pinned, engine)
		_, status, answer := sendImage(t, server, pushRef, "config", "layer one", "layer two")
		if status != http.StatusOK || !strings.Contains(answer, registryDigest) {
			t.Fatalf("%s: push = %d %s", name, status, answer)
		}
		if engine.pushed[pushRef] != "config+layer one+layer two" {
			t.Fatalf("%s: the registry got %q under %s", name, engine.pushed[pushRef], pushRef)
		}
		if !reflect.DeepEqual(*events, []PushEvent{{Repo: "registry.example.org/team/app", Tag: "2.0", Digest: registryDigest}}) {
			t.Fatalf("%s: recorded %+v", name, *events)
		}
		if !reflect.DeepEqual(engine.refs, want) {
			t.Fatalf("%s: the host's Docker holds %v afterwards, want %v\ncalls: %v", name, engine.refs, want, engine.calls)
		}
	}
}

// A second push sends only the blobs the first did not: the host's Docker
// keeps nothing, so the relay remembers what it was sent.
func TestPushSendsOnlyTheBlobsTheHostDoesNotHold(t *testing.T) {
	engine := &fakeEngine{refs: map[string]string{}}
	server, _ := relayFor(t, pinned, engine)
	if sent, status, answer := sendImage(t, server, pushRef, "config", "base layer", "layer one"); status != http.StatusOK || sent != 3 {
		t.Fatalf("first push sent %d blobs: %d %s", sent, status, answer)
	}
	sent, status, answer := sendImage(t, server, pushRef, "config two", "base layer", "layer two")
	if status != http.StatusOK || sent != 2 {
		t.Fatalf("second push sent %d blobs, want the 2 that changed: %d %s", sent, status, answer)
	}
	if engine.pushed[pushRef] != "config two+base layer+layer two" {
		t.Fatalf("the registry got %q", engine.pushed[pushRef])
	}
}

// A pull of an image the VM pushed takes it from what the host kept of the
// push: both sides already have all of it.
func TestPullOfAPushedImageTakesItFromWhatTheHostKept(t *testing.T) {
	engine := &fakeEngine{refs: map[string]string{}}
	server, _ := relayFor(t, pinned, engine)
	if _, status, answer := sendImage(t, server, pushRef, "config", "base layer", "layer one"); status != http.StatusOK {
		t.Fatalf("push = %d %s", status, answer)
	}
	engine.calls = nil
	status, answer, image := pullImage(t, server, pushRef)
	if status != http.StatusOK || image != "config+base layer+layer one" {
		t.Fatalf("pull of the pushed image = %d %s, the image %q", status, answer, image)
	}
	if !reflect.DeepEqual(engine.calls, []string{"inspect " + pushRef}) {
		t.Fatalf("the pull asked the host's Docker for %v, want only which image the tag names", engine.calls)
	}
}

// With keep_pushed set on the host, a pushed image stays in the host's
// Docker under its reference, to be let go of by the sweep in time, and the
// next push of it has the layers at hand.
func TestPushLeavesTheImageInTheHostsDockerWhenTheOwnerSaysSo(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	engine := &fakeEngine{refs: map[string]string{pushRef: "old+layer"}}
	cache := CacheFor("abc")
	server := httptest.NewServer(Handler(pinned, HostConfig{Keep: DefaultKeep, KeepPushed: true}, engine, cache, func(PushEvent) error { return nil }, func(err error) { t.Errorf("leftover: %v", err) }))
	defer server.Close()
	if _, status, answer := sendImage(t, server, pushRef, "config", "layer one"); status != http.StatusOK {
		t.Fatalf("push = %d %s", status, answer)
	}
	if engine.refs[pushRef] != "config+layer one" {
		t.Fatalf("the host's Docker holds %v after the push, want the pushed image under %s", engine.refs, pushRef)
	}
	if err := cache.Sweep(context.Background(), engine, time.Now().Add(5*time.Hour), DefaultKeep); err != nil {
		t.Fatal(err)
	}
	if _, held := engine.refs[pushRef]; held {
		t.Fatalf("the host's Docker still holds %v after the time", engine.refs)
	}
}

// Only a repository pinned with write is pushed to. Anything else is
// refused before a blob is taken, and never reaches the host's Docker.
func TestPushRefusesWhatIsNotPinnedForIt(t *testing.T) {
	for _, ref := range []string{
		pinnedRef,
		"registry.example.org/team/other:1",
		"registry.example.org/team/app/sub:1",
		"team/app:2.0",
		"registry.example.org/team/app",
	} {
		engine := &fakeEngine{refs: map[string]string{}}
		server, events := relayFor(t, pinned, engine)
		digests := []string{digestOf("config"), digestOf("layer")}
		for path, payload := range map[string]any{
			"/blobs/missing": map[string]any{"ref": ref, "digests": digests},
			"/push":          map[string]any{"ref": ref, "config": digests[0], "layers": digests[1:]},
		} {
			if status, answer := askJSON(t, server, path, payload); status != http.StatusForbidden && status != http.StatusBadRequest {
				t.Fatalf("%s for %s = %d %s, want it refused", path, ref, status, answer)
			}
		}
		if len(engine.calls) != 0 || len(*events) != 0 {
			t.Fatalf("push of %s reached the host's Docker: %v, recorded %v", ref, engine.calls, *events)
		}
	}

	readOnly := ProjectConfig{Repos: map[string]Access{"registry.example.org/team/base": AccessRead}}
	server, _ := relayFor(t, readOnly, &fakeEngine{})
	if status, answer := ask(t, server, http.MethodPut, "/blobs/"+digestOf("layer"), strings.NewReader("layer")); status != http.StatusForbidden {
		t.Fatalf("a project that pushes nowhere had a blob kept on the host: %d %s", status, answer)
	}
}

// The host keeps a blob only under the digest it hashes to, and pushes only
// blobs it holds: what goes to the registry is what was checked.
func TestPushTakesOnlyBlobsThatHashToTheirDigest(t *testing.T) {
	engine := &fakeEngine{refs: map[string]string{}}
	server, _ := relayFor(t, pinned, engine)
	claimed := digestOf("the layer")
	if status, answer := ask(t, server, http.MethodPut, "/blobs/"+claimed, strings.NewReader("something else")); status != http.StatusBadRequest {
		t.Fatalf("a blob under a digest it does not hash to = %d %s", status, answer)
	}
	if status, answer := ask(t, server, http.MethodPut, "/blobs/"+digestOf("config"), strings.NewReader("config")); status != http.StatusOK {
		t.Fatalf("config = %d %s", status, answer)
	}
	status, answer := askJSON(t, server, "/push", map[string]any{"ref": pushRef, "config": digestOf("config"), "layers": []string{claimed}})
	if status != http.StatusConflict || !strings.Contains(answer, claimed) {
		t.Fatalf("push with a blob the host does not hold = %d %s", status, answer)
	}
	if len(engine.calls) != 0 {
		t.Fatalf("the host's Docker was used: %v", engine.calls)
	}
}

// What the host's Docker says when the registry turns the push down is
// what the VM hears; nothing is recorded as pushed and the host's Docker
// is put back.
func TestPushSaysWhyTheHostCouldNotPush(t *testing.T) {
	engine := &fakeEngine{refs: map[string]string{}, pushErr: errors.New("denied: requested access to the resource is denied")}
	server, events := relayFor(t, pinned, engine)
	_, status, answer := sendImage(t, server, pushRef, "config", "layer")
	if status != http.StatusBadGateway || !strings.Contains(answer, "denied") {
		t.Fatalf("push = %d %s, want the host's error", status, answer)
	}
	if len(*events) != 0 || len(engine.refs) != 0 {
		t.Fatalf("after a failed push: recorded %v, the host's Docker holds %v", *events, engine.refs)
	}
}

// A push that could not be recorded is not reported as a success, and the
// answer says the image is in the registry all the same.
func TestPushThatCouldNotBeRecordedSaysSo(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	engine := &fakeEngine{refs: map[string]string{}}
	server := httptest.NewServer(Handler(pinned, HostConfig{Keep: DefaultKeep}, engine, CacheFor("abc"), func(PushEvent) error {
		return errors.New("the VM is gone")
	}, func(err error) { t.Errorf("leftover: %v", err) }))
	defer server.Close()
	_, status, answer := sendImage(t, server, pushRef, "config", "layer")
	if status != http.StatusInternalServerError || !strings.Contains(answer, "was pushed as "+registryDigest) || !strings.Contains(answer, "the VM is gone") {
		t.Fatalf("push = %d %s", status, answer)
	}
}
