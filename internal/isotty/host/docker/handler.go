package docker

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// PushEvent describes an image that reached its registry, so the caller
// can record it in the VM audit log.
type PushEvent struct {
	Repo   string
	Tag    string
	Digest string
}

type relay struct {
	cfg    ProjectConfig
	host   HostConfig
	engine Engine
	cache  Cache
	onPush func(PushEvent) error
	// onLeftover hears what went wrong on the host once the VM's answer is
	// settled: what its Docker could not be put back to, what could not be
	// recorded for later.
	onLeftover func(error)
	// One operation at a time: each one moves references in the host's
	// Docker and puts them back, and two at once would put back each
	// other's.
	mu sync.Mutex
}

// Handler serves the VM's `isotty-docker`; the host gateway mounts it under
// /docker. onPush records a push before the VM hears that it succeeded.
func Handler(cfg ProjectConfig, host HostConfig, engine Engine, cache Cache, onPush func(PushEvent) error, onLeftover func(error)) http.Handler {
	r := &relay{cfg: cfg, host: host, engine: engine, cache: cache, onPush: onPush, onLeftover: onLeftover}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		// The VM keeps what it receives as long as the host does.
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"repos": cfg.Repos, "keep": host.Keep.Seconds()})
	})
	mux.HandleFunc("POST /pull", r.pull)
	mux.HandleFunc("GET /blobs/{digest}", r.give)
	mux.HandleFunc("POST /blobs/missing", r.missing)
	mux.HandleFunc("PUT /blobs/{digest}", r.keep)
	mux.HandleFunc("POST /push", r.push)
	return mux
}

// pull gets the blobs of the image into the host's cache and tells the VM
// which they are, so that it fetches those it lacks and loads the image.
// The host is a way through, not a place to keep images: its Docker is
// left as it was found, and the cache holds the blobs for a while so that
// a pull run again, after a cut or for a tag that moved a little, does not
// fetch the whole image from the registry again.
func (r *relay) pull(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Ref      string `json:"ref"`
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil {
		gateway.WriteError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	repo, err := RepoOf(body.Ref)
	if err != nil {
		gateway.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !platformPattern.MatchString(body.Platform) {
		gateway.WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid platform %q", body.Platform))
		return
	}
	if _, ok := r.cfg.Access(repo); !ok {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("repository %s is not pinned for this project; the human pins it on the host with `isotty host docker add --repo %s`", repo, repo))
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	ctx := req.Context()
	answer := struct {
		Image
		// Pulled says why the host pulled from the registry although it
		// might have held the image, when that is so.
		Pulled string `json:"pulled,omitempty"`
	}{}
	// `docker manifest inspect` reaches the registry with the host's login
	// but on the CLI's own terms, not the daemon's: a registry the daemon
	// is set up to reach as insecure, it refuses. Then the pull decides.
	config, err := r.engine.Config(ctx, body.Ref, body.Platform)
	if err != nil {
		answer.Pulled = "the registry could not be asked which image the tag names without pulling it: " + err.Error()
	} else {
		image, held, err := r.cache.Image(config)
		if err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, "the host could not look into its cache: "+err.Error())
			return
		}
		if held {
			gateway.WriteJSON(w, http.StatusOK, image)
			return
		}
	}
	image, ok := r.fetch(w, ctx, body.Ref, body.Platform)
	if !ok {
		return
	}
	answer.Image = image
	gateway.WriteJSON(w, http.StatusOK, answer)
}

// fetch has the host's Docker pull the image and takes what it saves into
// the cache.
func (r *relay) fetch(w http.ResponseWriter, ctx context.Context, ref, platform string) (Image, bool) {
	before, err := r.engine.ImageID(ctx, ref)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the host's Docker did not answer: "+err.Error())
		return Image{}, false
	}
	if err := r.engine.Pull(ctx, ref, platform); err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the host's Docker could not pull the image: "+err.Error())
		return Image{}, false
	}
	// The VM may be gone by the time this runs, and the host's Docker is
	// put back all the same.
	defer func() {
		if err := r.restore(context.WithoutCancel(ctx), ref, before); err != nil {
			r.onLeftover(fmt.Errorf("put the host's Docker back after pulling %s: %w", ref, err))
		}
	}()

	archive, feed := io.Pipe()
	type taken struct {
		image Image
		err   error
	}
	done := make(chan taken, 1)
	go func() {
		image, err := r.cache.Take(archive)
		// Docker must not go on writing an archive nobody takes.
		archive.CloseWithError(err)
		done <- taken{image, err}
	}()
	err = r.engine.Save(ctx, ref, platform, feed)
	feed.CloseWithError(err)
	took := <-done
	if err := errors.Join(err, took.err); err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the host's Docker could not hand the image over: "+err.Error())
		return Image{}, false
	}
	return took.image, true
}

// give hands the VM one blob the host holds.
func (r *relay) give(w http.ResponseWriter, req *http.Request) {
	digest := req.PathValue("digest")
	if !digestPattern.MatchString(digest) {
		gateway.WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid digest %q", digest))
		return
	}
	held, err := r.cache.Has(digest)
	if err != nil {
		gateway.WriteError(w, http.StatusInternalServerError, "the host could not look into its cache: "+err.Error())
		return
	}
	if !held {
		gateway.WriteError(w, http.StatusNotFound, fmt.Sprintf("the host no longer holds %s; run the pull again", digest))
		return
	}
	blob, err := r.cache.Open(digest)
	if err != nil {
		gateway.WriteError(w, http.StatusInternalServerError, "the host could not open the blob: "+err.Error())
		return
	}
	defer blob.Close()
	info, err := blob.Stat()
	if err != nil {
		gateway.WriteError(w, http.StatusInternalServerError, "the host could not open the blob: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	// A copy that breaks off leaves the VM a short body, which does not
	// hash to the digest.
	_, _ = io.Copy(w, blob)
}

// restore leaves the reference naming what it named before the pull or
// the push, and removes the image that passed through unless something
// else in the host's Docker names it.
func (r *relay) restore(ctx context.Context, ref, before string) error {
	if before == "" {
		return r.engine.Remove(ctx, ref)
	}
	passing, err := r.engine.ImageID(ctx, ref)
	if err != nil {
		return err
	}
	if passing == before {
		return nil
	}
	if err := r.engine.Tag(ctx, before, ref); err != nil {
		return err
	}
	named, err := r.engine.Named(ctx, passing)
	if err != nil || named {
		return err
	}
	return r.engine.Remove(ctx, passing)
}

// pushable answers for a reference the VM wants to push, or refuses it.
func (r *relay) pushable(w http.ResponseWriter, ref string) (repo string, ok bool) {
	repo, err := RepoOf(ref)
	if err != nil {
		gateway.WriteError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	access, pinned := r.cfg.Access(repo)
	if !pinned {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("repository %s is not pinned for this project; the human pins it for pushing on the host with `isotty host docker add --repo %s --allow write`", repo, repo))
		return "", false
	}
	if !access.Covers(AccessWrite) {
		gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("repository %s is pinned for pulling only; the human allows pushing on the host with `isotty host docker add --repo %s --allow write`", repo, repo))
		return "", false
	}
	return repo, true
}

// missing tells which blobs of an image the host does not hold, so the VM
// sends those and no others. It refuses what push would refuse, before
// anything is sent.
func (r *relay) missing(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Ref     string   `json:"ref"`
		Digests []string `json:"digests"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&body); err != nil {
		gateway.WriteError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if _, ok := r.pushable(w, body.Ref); !ok {
		return
	}
	missing, ok := r.lacking(w, body.Digests)
	if !ok {
		return
	}
	gateway.WriteJSON(w, http.StatusOK, map[string]any{"missing": missing})
}

// lacking returns the digests the cache does not hold, each once.
func (r *relay) lacking(w http.ResponseWriter, digests []string) (lacking []string, ok bool) {
	lacking = []string{}
	seen := map[string]bool{}
	for _, digest := range digests {
		if !digestPattern.MatchString(digest) {
			gateway.WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid digest %q", digest))
			return nil, false
		}
		if seen[digest] {
			continue
		}
		seen[digest] = true
		held, err := r.cache.Has(digest)
		if err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, "the host could not look into its cache: "+err.Error())
			return nil, false
		}
		if !held {
			lacking = append(lacking, digest)
		}
	}
	return lacking, true
}

// keep takes one blob into the cache. Only a project that may push at all
// gets to use the host's disk.
func (r *relay) keep(w http.ResponseWriter, req *http.Request) {
	pushes := false
	for _, access := range r.cfg.Repos {
		pushes = pushes || access.Covers(AccessWrite)
	}
	if !pushes {
		gateway.WriteError(w, http.StatusForbidden, "no repository is pinned for pushing in this project")
		return
	}
	digest := req.PathValue("digest")
	if !digestPattern.MatchString(digest) {
		gateway.WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid digest %q", digest))
		return
	}
	switch err := r.cache.Put(digest, req.Body); {
	case errors.Is(err, errDigest):
		gateway.WriteError(w, http.StatusBadRequest, fmt.Sprintf("what was sent as %s does not hash to it", digest))
	case err != nil:
		gateway.WriteError(w, http.StatusInternalServerError, "the host could not keep the blob: "+err.Error())
	default:
		gateway.WriteJSON(w, http.StatusOK, map[string]any{})
	}
}

// push has the host's Docker load the image from the blobs the VM sent and
// push it. The reference is the one the pin was checked for: the archive
// Docker loads is written here and names nothing else, whatever the VM's
// own archive said. As with a pull, the host's Docker is left as it was
// found.
func (r *relay) push(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Ref string `json:"ref"`
		Image
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&body); err != nil {
		gateway.WriteError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	repo, ok := r.pushable(w, body.Ref)
	if !ok {
		return
	}
	lacking, ok := r.lacking(w, body.Blobs())
	if !ok {
		return
	}
	if len(lacking) > 0 {
		gateway.WriteError(w, http.StatusConflict, fmt.Sprintf("the host does not hold %s of the image; run the push again, which sends what is missing", strings.Join(lacking, ", ")))
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	ctx := req.Context()
	before, err := r.engine.ImageID(ctx, body.Ref)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the host's Docker did not answer: "+err.Error())
		return
	}
	archive, feed := io.Pipe()
	written := make(chan error, 1)
	go func() {
		err := r.writeArchive(feed, body.Ref, body.Image)
		feed.CloseWithError(err)
		written <- err
	}()
	err = r.engine.Load(ctx, archive)
	// Docker may stop reading before the archive ends, and the writer must
	// not wait for it.
	archive.Close()
	if err := errors.Join(err, <-written); err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the host's Docker could not take the image in: "+err.Error())
		return
	}
	// The VM may be gone by the time this runs, and the host's Docker is
	// put back all the same. Or, when the owner of this machine has it so,
	// the pushed image stays under the reference, for the next push of it
	// to load and compress only what changed.
	kept := false
	defer func() {
		ctx := context.WithoutCancel(ctx)
		if kept {
			image, err := r.engine.ImageID(ctx, body.Ref)
			if err == nil {
				err = r.cache.KeepPushed(body.Ref, image)
			}
			if err != nil {
				r.onLeftover(fmt.Errorf("record that %s stays in the host's Docker: %w", body.Ref, err))
			}
			return
		}
		if err := r.restore(ctx, body.Ref, before); err != nil {
			r.onLeftover(fmt.Errorf("put the host's Docker back after pushing %s: %w", body.Ref, err))
		}
	}()

	digest, err := r.engine.Push(ctx, body.Ref)
	if err != nil {
		gateway.WriteError(w, http.StatusBadGateway, "the host's Docker could not push the image: "+err.Error())
		return
	}
	if err := r.onPush(PushEvent{Repo: repo, Tag: body.Ref[len(repo)+1:], Digest: digest}); err != nil {
		gateway.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("%s was pushed as %s but recording the audit event failed: %v", body.Ref, digest, err))
		return
	}
	// The registry now serves this image under the reference, and the host
	// holds all of it: a pull of it finds it here, as after a pull.
	if err := r.cache.keep(body.Image); err != nil {
		r.onLeftover(fmt.Errorf("record the pushed image %s for pulls: %w", body.Ref, err))
	}
	kept = r.host.KeepPushed
	gateway.WriteJSON(w, http.StatusOK, map[string]string{"ref": body.Ref, "digest": digest})
}

// writeArchive writes what `docker load` reads: the blobs under the names
// Docker's own archives give them, and a manifest.json that puts them
// together as one image under the reference.
func (r *relay) writeArchive(w io.Writer, ref string, image Image) error {
	name := func(digest string) string {
		return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
	}
	entry := struct {
		Config   string
		RepoTags []string
		Layers   []string
	}{Config: name(image.Config), RepoTags: []string{ref}, Layers: []string{}}
	for _, layer := range image.Layers {
		entry.Layers = append(entry.Layers, name(layer))
	}
	manifest, err := json.Marshal([]any{entry})
	if err != nil {
		return err
	}
	archive := tar.NewWriter(w)
	if err := archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifest))}); err != nil {
		return err
	}
	if _, err := archive.Write(manifest); err != nil {
		return err
	}
	for _, digest := range image.Blobs() {
		if err := r.writeBlob(archive, name(digest), digest); err != nil {
			return err
		}
	}
	return archive.Close()
}

func (r *relay) writeBlob(archive *tar.Writer, name, digest string) error {
	blob, err := r.cache.Open(digest)
	if err != nil {
		return err
	}
	defer blob.Close()
	info, err := blob.Stat()
	if err != nil {
		return err
	}
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: info.Size()}); err != nil {
		return err
	}
	_, err = io.Copy(archive, blob)
	return err
}
