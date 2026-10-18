package image

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A registry that accepts a push, and remembers what it was given.
type fakeRegistry struct {
	mu sync.Mutex

	blobs     map[string][]byte
	manifests map[string]string
	// have is a blob the registry already holds, which must not be uploaded a
	// second time.
	have string
	// wantAuth turns on the bearer-token dance.
	wantAuth bool
	uploads  int
	// realm is the server's own address, filled in once it has one.
	realm string
}

func (f *fakeRegistry) handler(t *testing.T) http.Handler {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		// The scope the registry asked for is the scope it must be given back;
		// a pull-only token is the failure this whole dance exists to avoid.
		if !strings.Contains(r.URL.Query().Get("scope"), "push") {
			t.Errorf("token requested for scope %q, which cannot push",
				r.URL.Query().Get("scope"))
		}

		_ = json.NewEncoder(w).Encode(map[string]string{"token": "a-token"})
	})

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if f.wantAuth && r.Header.Get("Authorization") != "Bearer a-token" {
			w.Header().Set("WWW-Authenticate",
				`Bearer realm="`+f.realm+`/token",service="fake",scope="repository:app:pull,push"`)
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		f.mu.Lock()
		defer f.mu.Unlock()

		switch {
		case strings.Contains(r.URL.Path, "/blobs/uploads/"):
			f.uploads++
			w.Header().Set("Location", f.realm+"/v2/app/blobs/upload/1")
			w.WriteHeader(http.StatusAccepted)

		case strings.Contains(r.URL.Path, "/blobs/upload/"):
			body, _ := io.ReadAll(r.Body)
			f.blobs[r.URL.Query().Get("digest")] = body
			w.WriteHeader(http.StatusCreated)

		case strings.Contains(r.URL.Path, "/blobs/") && r.Method == http.MethodHead:
			if strings.HasSuffix(r.URL.Path, f.have) && f.have != "" {
				w.WriteHeader(http.StatusOK)

				return
			}

			w.WriteHeader(http.StatusNotFound)

		case strings.Contains(r.URL.Path, "/manifests/"):
			body, _ := io.ReadAll(r.Body)
			at := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			f.manifests[at] = string(body)
			w.WriteHeader(http.StatusCreated)

		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	return mux
}

// An image written as a layout is pushed blob by blob, then named.
//
// **`--push` was accepted and did nothing.** The flag was parsed, the build
// succeeded, and a note said the image had not been published - which is honest
// and is not what anyone who wrote `--push` was asking for.
func TestALayoutIsPushedBlobsFirstThenTheManifest(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{blobs: map[string][]byte{}, manifests: map[string]string{}}
	srv := httptest.NewServer(reg.handler(t))

	defer srv.Close()

	reg.realm = srv.URL

	dir := writeATinyLayout(t, "app:latest")

	got, err := Push(context.Background(), dir,
		strings.TrimPrefix(srv.URL, "http://")+"/app:latest",
		PushOptions{Client: srv.Client(), Plain: true})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	// The config and the one layer, and the manifest under its tag.
	if len(reg.blobs) != 2 {
		t.Errorf("pushed %d blobs, wanted the config and the layer: %v",
			len(reg.blobs), keysOf(reg.blobs))
	}

	if _, ok := reg.manifests["latest"]; !ok {
		t.Errorf("no manifest was put under the tag: %v", reg.manifests)
	}

	if got == "" || !strings.HasPrefix(got, "sha256:") {
		t.Errorf("push reported %q, wanted the manifest digest", got)
	}

	// Every blob arrives under the digest of its own bytes, which is the only
	// thing a registry checks and the only thing that makes a push verifiable.
	for digest, body := range reg.blobs {
		if want := DigestOf(body); want != digest {
			t.Errorf("a blob was sent as %s but its bytes are %s", digest, want)
		}
	}
}

// A blob the registry already holds is not sent again. A base layer is most of
// an image and most pushes share one.
func TestABlobTheRegistryHasIsNotSentAgain(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{blobs: map[string][]byte{}, manifests: map[string]string{}}
	srv := httptest.NewServer(reg.handler(t))

	defer srv.Close()

	reg.realm = srv.URL

	dir := writeATinyLayout(t, "app:latest")
	reg.have = firstLayerDigest(t, dir)

	_, err := Push(context.Background(), dir,
		strings.TrimPrefix(srv.URL, "http://")+"/app:latest",
		PushOptions{Client: srv.Client(), Plain: true})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	if reg.uploads != 1 {
		t.Errorf("started %d uploads, wanted 1 - the layer was already there", reg.uploads)
	}
}

// A registry that challenges is answered with a token carrying the push scope.
func TestAChallengingRegistryIsGivenAPushToken(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{blobs: map[string][]byte{}, manifests: map[string]string{}, wantAuth: true}
	srv := httptest.NewServer(reg.handler(t))

	defer srv.Close()

	reg.realm = srv.URL

	dir := writeATinyLayout(t, "app:latest")

	_, err := Push(context.Background(), dir,
		strings.TrimPrefix(srv.URL, "http://")+"/app:latest",
		PushOptions{Client: srv.Client(), Plain: true, Challenges: t.TempDir()})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	if len(reg.blobs) != 2 {
		t.Errorf("pushed %d blobs behind a challenge", len(reg.blobs))
	}
}

func writeATinyLayout(t *testing.T, ref string) string {
	t.Helper()

	dir := t.TempDir()

	err := WriteLayout(dir, Spec{
		Ref:      ref,
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		Layers: []LayerSource{
			func(w io.Writer) error {
				_, err := w.Write([]byte("not a real tar, and nothing here reads it as one"))

				return err
			},
		},
	})
	if err != nil {
		t.Fatalf("write the layout: %v", err)
	}

	return dir
}

func firstLayerDigest(t *testing.T, dir string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}

	var index ocispec.Index

	err = json.Unmarshal(raw, &index)
	if err != nil {
		t.Fatal(err)
	}

	m := readBlobAs[ocispec.Manifest](t, dir, string(index.Manifests[0].Digest))

	return string(m.Layers[0].Digest)
}

func readBlobAs[T any](t *testing.T, dir, digest string) T {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(dir, "blobs", "sha256",
		strings.TrimPrefix(digest, "sha256:")))
	if err != nil {
		t.Fatal(err)
	}

	var out T

	err = json.Unmarshal(raw, &out)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
