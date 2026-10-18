package image

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// layoutHolding writes an OCI layout whose one layer holds name = body, at dir.
func layoutHolding(t *testing.T, dir, ref, name, body string) {
	t.Helper()

	err := WriteLayout(dir, Spec{
		Ref:      ref,
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		Layers: []LayerSource{func(w io.Writer) error {
			tw := tar.NewWriter(w)

			err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))})
			if err != nil {
				return err
			}

			_, err = tw.Write([]byte(body))
			if err != nil {
				return err
			}

			return tw.Close()
		}},
	})
	if err != nil {
		t.Fatalf("write the layout: %v", err)
	}
}

// noNetwork fails any request, because a pinned local image needs none.
type noNetwork struct{ t *testing.T }

func (n noNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("a pinned image this machine saved went to the network: %s", r.URL)

	return nil, errors.New("no network in this test")
}

// A pinned image this machine saved is pulled from the store, with no registry.
//
// **A digest cannot be wrong, so the local copy is as good as any.** The
// midnight-node warm path saved an image with SAVE IMAGE and named it in a
// later FROM; rth went to Docker Hub for it and got a 401. A tag stays remote -
// it moves - but the digest SAVE IMAGE prints names bytes this machine holds.
func TestAPinnedImageIsServedFromTheLocalStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layout := filepath.Join(root, LayoutName("nowhere.invalid/app:warm"))
	layoutHolding(t, layout, "nowhere.invalid/app:warm", "greeting", "from the store\n")

	digest, err := SaveLocal(layout, root)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	_, err = Pull(context.Background(), "nowhere.invalid/app@"+digest, dir, Options{
		Local: root, Platform: "linux/amd64", Client: &http.Client{Transport: noNetwork{t}},
	})
	if err != nil {
		t.Fatalf("pull a pinned image this machine saved: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "greeting"))
	if err != nil || string(b) != "from the store\n" {
		t.Errorf("the layer did not land: %q, %v", b, err)
	}
}

// And the store is verified like a registry: a blob that changed is refused.
func TestACorruptLocalBlobIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layout := filepath.Join(root, LayoutName("nowhere.invalid/app:warm"))
	layoutHolding(t, layout, "nowhere.invalid/app:warm", "greeting", "genuine\n")

	digest, err := SaveLocal(layout, root)
	if err != nil {
		t.Fatal(err)
	}

	layer := firstLayerDigest(t, layout)
	at := filepath.Join(root, localBlobs, strings.Replace(layer, ":", string(filepath.Separator), 1))

	// Replaced rather than written through: the store links blobs, and writing
	// through a link would corrupt the layout it came from as well.
	err = os.Remove(at)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(at, []byte("substituted"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Pull(context.Background(), "nowhere.invalid/app@"+digest, t.TempDir(), Options{
		Local: root, Platform: "linux/amd64", Client: &http.Client{Transport: noNetwork{t}},
	})
	if err == nil {
		t.Fatal("a local blob whose bytes no longer match its digest was used")
	}
}

// A tag is always asked of its registry, even when this machine saved one.
//
// **Tags move, so the store cannot answer for one.** What it can do is say so
// when the registry does not have it: the failure names the digest SAVE IMAGE
// wrote, and how to pin to it.
func TestATagIsAskedOfTheRegistryAndTheFailureNamesTheLocalDigest(t *testing.T) {
	t.Parallel()

	var asked atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))

	defer srv.Close()

	ref := strings.TrimPrefix(srv.URL, "http://") + "/app:warm"

	root := t.TempDir()
	layout := filepath.Join(root, LayoutName(ref))
	layoutHolding(t, layout, ref, "greeting", "saved\n")

	digest, err := SaveLocal(layout, root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Pull(context.Background(), ref, t.TempDir(), Options{
		Local: root, Platform: "linux/amd64", Plain: true, Client: srv.Client(),
	})
	if err == nil {
		t.Fatal("a tag the registry does not have was pulled")
	}

	if asked.Load() == 0 {
		t.Error("the registry was never asked about a tag; tags move, so the store cannot answer")
	}

	for _, want := range []string{"SAVE IMAGE", "@" + digest, "pin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not say %q:\n%v", want, err)
		}
	}
}

// A manifest filed under a digest it does not hash to is never used.
//
// **The digest is the whole claim.** A valid manifest for a different image,
// sitting where the pinned one should be, parses perfectly and names blobs the
// store holds - so nothing but the check against the digest stops the build
// getting the wrong image under the right name.
func TestASwappedLocalManifestIsNotTrusted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	pinned := filepath.Join(root, LayoutName("nowhere.invalid/app:one"))
	layoutHolding(t, pinned, "nowhere.invalid/app:one", "which", "the pinned image\n")

	digest, err := SaveLocal(pinned, root)
	if err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(root, LayoutName("nowhere.invalid/app:two"))
	layoutHolding(t, other, "nowhere.invalid/app:two", "which", "another image\n")

	otherDigest, err := SaveLocal(other, root)
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(localBlobPath(root, otherDigest))
	if err != nil {
		t.Fatal(err)
	}

	at := localBlobPath(root, digest)

	err = os.Remove(at)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(at, body, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	_, err = Pull(context.Background(), "nowhere.invalid/app@"+digest, dir, Options{
		Local: root, Platform: "linux/amd64", Client: &http.Client{Transport: refuseQuietly{}},
	})
	if err == nil {
		b, _ := os.ReadFile(filepath.Join(dir, "which"))
		t.Fatalf("pulled %q under a digest whose manifest was swapped for another image's", b)
	}
}

// refuseQuietly fails any request without failing the test: here going to the
// network is the right answer, and the absent registry is what stops it.
type refuseQuietly struct{}

func (refuseQuietly) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in this test")
}
