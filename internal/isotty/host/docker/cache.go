package docker

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const sweepEvery = 10 * time.Minute

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var errDigest = errors.New("the content does not hash to its digest")

// Cache holds the blobs of the images that pass between a project's VM and
// its registries. The host's Docker keeps nothing of a pull or a push, so
// without it every pull would fetch the whole image from the registry again
// and every push would carry every layer again; with it a pull that is run
// again, or a push of an image that changed a little, moves only what is
// new. It is all a pull or a push leaves on the host: under the temporary
// directory, where the OS clears what IsoTTY did not get to, and per
// project, so that a VM can only push what it sent itself and only receive
// what was pulled for it.
type Cache struct {
	root string
	dir  string
}

// Image is an image as the archives Docker saves and loads put it together:
// the digests of its config and of its layers, in order.
type Image struct {
	Config string   `json:"config"`
	Layers []string `json:"layers"`
}

// Blobs lists the image's blobs, each once.
func (i Image) Blobs() []string {
	blobs := []string{}
	seen := map[string]bool{}
	for _, digest := range append([]string{i.Config}, i.Layers...) {
		if !seen[digest] {
			seen[digest] = true
			blobs = append(blobs, digest)
		}
	}
	return blobs
}

func CacheFor(projectHash string) Cache {
	root := filepath.Join(os.TempDir(), rootName())
	return Cache{root: root, dir: filepath.Join(root, "docker", projectHash)}
}

func (c Cache) path(digest string) string {
	return filepath.Join(c.dir, strings.TrimPrefix(digest, "sha256:"))
}

// Has reports whether the blob is held, which counts as a use of it.
func (c Cache) Has(digest string) (bool, error) {
	now := time.Now()
	err := os.Chtimes(c.path(digest), now, now)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Put keeps what r yields under the digest, which it must hash to.
func (c Cache) Put(digest string, r io.Reader) error {
	if err := c.prepare(); err != nil {
		return err
	}
	incoming, err := os.CreateTemp(c.dir, "incoming-")
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(incoming, hash), r)
	if closeErr := incoming.Close(); err == nil {
		err = closeErr
	}
	if err == nil && "sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest {
		err = errDigest
	}
	if err == nil {
		err = os.Rename(incoming.Name(), c.path(digest))
	}
	if err != nil {
		return errors.Join(err, os.Remove(incoming.Name()))
	}
	return nil
}

// prepare makes the project's directory inside one that only this user can
// enter. The temporary directory is shared between users on Linux, and in
// a directory someone else made under the same name they could read the
// layers, or swap one after its digest was checked.
func (c Cache) prepare() error {
	if err := os.Mkdir(c.root, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := os.Lstat(c.root)
	if err != nil {
		return err
	}
	if !private(info) {
		return fmt.Errorf("%s is not a directory that only this user can enter; remove it", c.root)
	}
	return os.MkdirAll(c.dir, 0o700)
}

func (c Cache) Open(digest string) (*os.File, error) {
	return os.Open(c.path(digest))
}

// blobName is how an archive of Docker 25 or newer names a blob.
var blobName = regexp.MustCompile(`^blobs/sha256/([0-9a-f]{64})$`)

// Take reads an archive `docker save` wrote, keeps its blobs and records
// its image, which it returns.
func (c Cache) Take(archive io.Reader) (Image, error) {
	var manifest []struct {
		Config string
		Layers []string
	}
	seen := false
	reader := tar.NewReader(archive)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Image{}, err
		}
		if header.Name == "manifest.json" {
			seen = true
			if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
				return Image{}, fmt.Errorf("manifest.json of the archive: %w", err)
			}
			continue
		}
		if match := blobName.FindStringSubmatch(header.Name); match != nil {
			if err := c.Put("sha256:"+match[1], reader); err != nil {
				return Image{}, fmt.Errorf("%s of the archive: %w", header.Name, err)
			}
		}
	}
	if !seen || len(manifest) != 1 {
		return Image{}, fmt.Errorf("the archive holds %d images, not one", len(manifest))
	}
	image := Image{Layers: []string{}}
	for i, name := range append([]string{manifest[0].Config}, manifest[0].Layers...) {
		match := blobName.FindStringSubmatch(name)
		if match == nil {
			return Image{}, fmt.Errorf("the archive names %s, not blobs by their digests; Docker 25 or newer does", name)
		}
		if i == 0 {
			image.Config = "sha256:" + match[1]
		} else {
			image.Layers = append(image.Layers, "sha256:"+match[1])
		}
	}
	return image, c.keep(image)
}

func (c Cache) recordPath(config string) string {
	return filepath.Join(c.dir, "image-"+strings.TrimPrefix(config, "sha256:"))
}

// keep records the image under its config, so that a later pull of the same
// image finds its blobs without the registry.
func (c Cache) keep(image Image) error {
	return c.write(c.recordPath(image.Config), image)
}

// write puts the record at path in one step.
func (c Cache) write(path string, record any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	incoming, err := os.CreateTemp(c.dir, "incoming-")
	if err != nil {
		return err
	}
	_, err = incoming.Write(data)
	if closeErr := incoming.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(incoming.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(incoming.Name()))
	}
	return nil
}

// Image returns the image recorded under the config, when it is recorded
// and every one of its blobs is held; that counts as a use of them all.
func (c Cache) Image(config string) (image Image, held bool, err error) {
	record, err := os.ReadFile(c.recordPath(config))
	if errors.Is(err, fs.ErrNotExist) {
		return Image{}, false, nil
	}
	if err != nil {
		return Image{}, false, err
	}
	if err := json.Unmarshal(record, &image); err != nil {
		return Image{}, false, fmt.Errorf("%s: %w", c.recordPath(config), err)
	}
	for _, digest := range image.Blobs() {
		if held, err := c.Has(digest); !held || err != nil {
			return Image{}, false, err
		}
	}
	now := time.Now()
	return image, true, os.Chtimes(c.recordPath(config), now, now)
}

// pushedPath names the record of an image kept in the host's Docker under
// the reference.
func (c Cache) pushedPath(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(c.dir, "pushed-"+hex.EncodeToString(sum[:]))
}

type pushed struct {
	Ref   string `json:"ref"`
	Image string `json:"image"`
}

// KeepPushed records that the host's Docker was left holding the image
// under the reference, from when it is to be let go again.
func (c Cache) KeepPushed(ref, image string) error {
	if err := c.prepare(); err != nil {
		return err
	}
	return c.write(c.pushedPath(ref), pushed{Ref: ref, Image: image})
}

// release takes a kept image out of the host's Docker, unless the
// reference has since been moved to another image, which is then the
// owner's and stays.
func (c Cache) release(ctx context.Context, engine Engine, record string) error {
	data, err := os.ReadFile(record)
	if err != nil {
		return err
	}
	var kept pushed
	if err := json.Unmarshal(data, &kept); err != nil {
		return fmt.Errorf("%s: %w", record, err)
	}
	image, err := engine.ImageID(ctx, kept.Ref)
	if err != nil {
		return err
	}
	if image == kept.Image {
		if err := engine.Remove(ctx, kept.Ref); err != nil {
			return err
		}
	}
	return os.Remove(record)
}

// Sweep lets go of what was last used more than life before now: blobs, a
// transfer that never finished included, and images kept in the host's
// Docker.
func (c Cache) Sweep(ctx context.Context, engine Engine, now time.Time, life time.Duration) error {
	entries, err := os.ReadDir(c.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			// A transfer finished meanwhile and the blob took its own name.
			continue
		}
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) <= life {
			continue
		}
		path := filepath.Join(c.dir, entry.Name())
		if strings.HasPrefix(entry.Name(), "pushed-") {
			err = c.release(ctx, engine, path)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// KeepSwept sweeps now and then at intervals until stop is called, so that
// an attach left open for days does not keep things for days. onError
// hears the first failure, after which nothing more is swept.
func (c Cache) KeepSwept(engine Engine, life time.Duration, onError func(error)) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(sweepEvery)
		defer ticker.Stop()
		for {
			if err := c.Sweep(context.Background(), engine, time.Now(), life); err != nil {
				onError(fmt.Errorf("sweep what the host keeps of the Docker images in %s: %w", c.dir, err))
				return
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// Remove lets go of everything held for the project: the images kept in
// the host's Docker, then the directory. An image that could not be let go
// is named in the error, and the directory goes all the same, so that a
// project that is taken down leaves nothing behind that IsoTTY still
// tracks.
func (c Cache) Remove(ctx context.Context, engine Engine) error {
	records, err := filepath.Glob(filepath.Join(c.dir, "pushed-*"))
	if err != nil {
		return err
	}
	var errs []error
	for _, record := range records {
		if err := c.release(ctx, engine, record); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, os.RemoveAll(c.dir))...)
}
