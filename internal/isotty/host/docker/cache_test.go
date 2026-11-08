package docker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func cacheIn(t *testing.T) Cache {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	return CacheFor("abc")
}

func put(t *testing.T, cache Cache, content string) string {
	t.Helper()
	digest := digestOf(content)
	if err := cache.Put(digest, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	return digest
}

func holds(t *testing.T, cache Cache, digest string) bool {
	t.Helper()
	blob, err := cache.Open(digest)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatal(err)
	}
	blob.Close()
	return true
}

// A blob stays for the configured time after its last use and no longer,
// so the host's disk holds only what the pulls and pushes of the last hours
// needed.
func TestCacheForgetsABlobFourHoursAfterItsLastUse(t *testing.T) {
	cache := cacheIn(t)
	used, unused := put(t, cache, "used"), put(t, cache, "unused")
	now := time.Now()

	if err := cache.Sweep(context.Background(), &fakeEngine{}, now.Add(3*time.Hour), DefaultKeep); err != nil {
		t.Fatal(err)
	}
	if !holds(t, cache, used) || !holds(t, cache, unused) {
		t.Fatal("a blob was forgotten within four hours of being sent")
	}

	long := now.Add(-5 * time.Hour)
	for _, digest := range []string{used, unused} {
		if err := os.Chtimes(cache.path(digest), long, long); err != nil {
			t.Fatal(err)
		}
	}
	if held, err := cache.Has(used); err != nil || !held {
		t.Fatalf("Has = %v, %v", held, err)
	}
	if err := cache.Sweep(context.Background(), &fakeEngine{}, now, DefaultKeep); err != nil {
		t.Fatal(err)
	}
	if !holds(t, cache, used) {
		t.Fatal("a blob a push asked about was forgotten")
	}
	if holds(t, cache, unused) {
		t.Fatal("a blob nothing used for five hours is still held")
	}
}

// An attach sweeps when it begins, so what a past session left behind goes
// without waiting for a push.
func TestCacheIsSweptWhenKeptSwept(t *testing.T) {
	cache := cacheIn(t)
	stale := put(t, cache, "stale")
	long := time.Now().Add(-5 * time.Hour)
	if err := os.Chtimes(cache.path(stale), long, long); err != nil {
		t.Fatal(err)
	}
	stop := cache.KeepSwept(&fakeEngine{}, DefaultKeep, func(err error) { t.Errorf("sweep: %v", err) })
	stop()
	if holds(t, cache, stale) {
		t.Fatal("the stale blob outlived the sweep")
	}
}

// `isotty down` takes the project's blobs with it, and no other project's.
func TestCacheRemoveDropsTheProjectsBlobsOnly(t *testing.T) {
	cache := cacheIn(t)
	other := CacheFor("def")
	mine, theirs := put(t, cache, "mine"), put(t, other, "theirs")
	if err := cache.Remove(context.Background(), &fakeEngine{}); err != nil {
		t.Fatal(err)
	}
	if holds(t, cache, mine) || !holds(t, other, theirs) {
		t.Fatal("Remove did not drop exactly the project's blobs")
	}
}

// The temporary directory may be shared with other users, so blobs are
// kept only inside a directory that this user alone can enter.
func TestCacheRefusesADirectoryOthersCanEnter(t *testing.T) {
	cache := cacheIn(t)
	if err := os.Mkdir(filepath.Join(os.TempDir(), rootName()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(digestOf("layer"), strings.NewReader("layer")); err == nil {
		t.Fatal("a blob was kept in a directory others can enter")
	}

	put(t, cacheIn(t), "layer")
	info, err := os.Stat(filepath.Join(os.TempDir(), rootName()))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the directory the cache made: %v, %v", info.Mode(), err)
	}
}

// An image taken from an archive is found again under its config as long as
// every blob of it is held; a blob that went takes the image with it, so
// that nothing is served in part.
func TestCacheKnowsAnImageWhileItHoldsAllOfIt(t *testing.T) {
	cache := cacheIn(t)
	image, err := cache.Take(bytes.NewReader(archiveOf("config+base layer+layer one", "registry.example.org/team/base:1.2")))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Image{Config: digestOf("config"), Layers: []string{digestOf("base layer"), digestOf("layer one")}}); !reflect.DeepEqual(image, want) {
		t.Fatalf("taken %+v, want %+v", image, want)
	}
	for _, digest := range image.Blobs() {
		if !holds(t, cache, digest) {
			t.Fatalf("%s was not kept", digest)
		}
	}
	if found, held, err := cache.Image(image.Config); err != nil || !held || !reflect.DeepEqual(found, image) {
		t.Fatalf("Image = %+v, %v, %v", found, held, err)
	}
	if err := os.Remove(cache.path(digestOf("layer one"))); err != nil {
		t.Fatal(err)
	}
	if _, held, err := cache.Image(image.Config); err != nil || held {
		t.Fatalf("Image with a blob gone = held %v, %v", held, err)
	}
	if _, held, err := cache.Image(digestOf("never taken")); err != nil || held {
		t.Fatalf("Image never taken = held %v, %v", held, err)
	}
}

// An image the host's Docker was left holding after a push goes when the
// configured time has passed since that push, unless its reference has
// since been moved to another image, which is the owner's; `isotty down`
// lets go of them all.
func TestCacheLetsGoOfKeptImagesInTime(t *testing.T) {
	cache := cacheIn(t)
	engine := &fakeEngine{refs: map[string]string{pushRef: "pushed", pinnedRef: "pushed too", "mine:1": "pushed"}}
	for _, ref := range []string{pushRef, pinnedRef} {
		if err := cache.KeepPushed(ref, engine.refs[ref]); err != nil {
			t.Fatal(err)
		}
	}
	if err := cache.Sweep(context.Background(), engine, time.Now().Add(3*time.Hour), DefaultKeep); err != nil {
		t.Fatal(err)
	}
	if len(engine.refs) != 3 {
		t.Fatalf("an image was let go of within the time: %v", engine.refs)
	}
	engine.refs[pinnedRef] = "the owner's own"
	if err := cache.Sweep(context.Background(), engine, time.Now().Add(5*time.Hour), DefaultKeep); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{pinnedRef: "the owner's own", "mine:1": "pushed"}; !reflect.DeepEqual(engine.refs, want) {
		t.Fatalf("the host's Docker holds %v after the sweep, want %v", engine.refs, want)
	}

	if err := cache.KeepPushed(pushRef, "pushed again"); err != nil {
		t.Fatal(err)
	}
	engine.refs[pushRef] = "pushed again"
	if err := cache.Remove(context.Background(), engine); err != nil {
		t.Fatal(err)
	}
	if _, held := engine.refs[pushRef]; held {
		t.Fatalf("Remove left %s in the host's Docker", pushRef)
	}
	if _, err := os.Stat(cache.dir); !os.IsNotExist(err) {
		t.Fatalf("Remove left the directory: %v", err)
	}
}
