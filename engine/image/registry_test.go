package image_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// fakeRegistry serves an image, and can be told to corrupt what it returns.
//
// A real registry is exercised by a separate, network-dependent test. This one
// exists so the *refusals* can be tested: nobody can ask Docker Hub to serve a
// corrupt blob on demand.
type fakeRegistry struct {
	layers  [][]byte // compressed tars, per mediaType below
	corrupt bool
	// mediaType is what the manifest declares each layer to be. Empty means
	// gzip, which every existing case here serves.
	mediaType string
	served    int

	// inFlight counts blob requests being served at this moment, and mostBlobs
	// the highest that ever was. Layers are independent objects and fetching
	// them one after another spends the whole of a pull waiting (E641).
	blobMu    sync.Mutex
	inFlight  int
	mostBlobs int
	blobDelay time.Duration
	// config is the image configuration blob, as JSON. Empty serves `{}`, which
	// is what an image with nothing declared looks like.
	config []byte
	// multi serves a manifest list rather than a manifest, which is what a
	// multi-platform tag names.
	multi bool
	// manifests counts manifest requests, separately from blobs: resolving a
	// reference fetches no blob at all, so a blob counter cannot tell "resolved
	// without asking" from "did not resolve".
	manifests int
	// auth makes this registry behave like a real one: an unauthenticated
	// request is answered with a challenge and nothing else, and a token has to
	// be fetched from the realm it names. Every test here predates this and runs
	// without it, which is why the exchange that costs a no-op build 0.465s was
	// unguarded (E534).
	auth bool
	// probes counts unauthenticated requests - the round trips that fetch no
	// data - separately from tokens.
	probes int
	tokens int
	// pings counts requests to `/v2/` itself - the registry's own endpoint,
	// which this engine asks for nothing except a warm connection.
	pings int
}

func gzipTar(t *testing.T, name, body string) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []interface{ Close() error }{tw, zw} {
		err := c.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	return buf.Bytes()
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)

	return "sha256:" + hex.EncodeToString(sum[:])
}

// configBlob is what this registry serves as the image configuration.
func (f *fakeRegistry) configBlob() []byte {
	if len(f.config) == 0 {
		return []byte("{}")
	}

	return f.config
}

// layerType is what the manifest declares, defaulting to gzip.
func (f *fakeRegistry) layerType() string {
	if f.mediaType == "" {
		return "application/vnd.oci.image.layer.v1.tar+gzip"
	}

	return f.mediaType
}

func (f *fakeRegistry) start(t *testing.T) string {
	t.Helper()

	mux := http.NewServeMux()

	// Set once the server exists, because the challenge has to name its own
	// realm and the URL is not known until then.
	var realm string

	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		f.tokens++

		_ = json.NewEncoder(w).Encode(map[string]any{"token": "issued"})
	})

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		// The ping. Counted apart from the probe: one is a connection being
		// warmed, the other is a round trip fetching a challenge.
		if r.URL.Path == "/v2/" {
			f.pings++

			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		if f.auth && r.Header.Get("Authorization") == "" {
			f.probes++

			w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+
				`",service="fake",scope="repository:thing:pull"`)
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			f.manifests++

			// A manifest list, when the tag names one and the request is for
			// the tag rather than for one of the images it lists.
			if f.multi && !strings.Contains(r.URL.Path, "/manifests/sha256:") {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
				_ = json.NewEncoder(w).Encode(map[string]any{
					testSchemaVersion: 2,
					testMediaType:     ocispec.MediaTypeImageIndex,
					"manifests": []map[string]any{
						{
							testMediaType: ocispec.MediaTypeImageManifest,
							testDigest:    "sha256:" + strings.Repeat("a", 64),
							testSize:      2,
							"platform":    map[string]any{"os": "linux", "architecture": "amd64"},
						},
						{
							testMediaType: ocispec.MediaTypeImageManifest,
							testDigest:    "sha256:" + strings.Repeat("b", 64),
							testSize:      2,
							"platform":    map[string]any{"os": "linux", "architecture": "arm64"},
						},
					},
				})

				return
			}

			descs := make([]map[string]any, 0, len(f.layers))
			for _, l := range f.layers {
				descs = append(descs, map[string]any{
					testMediaType: f.layerType(),
					testDigest:    digestOf(l),
					testSize:      len(l),
				})
			}

			cfg := f.configBlob()

			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				testSchemaVersion: 2,
				testMediaType:     ocispec.MediaTypeImageManifest,
				testConfigField:   map[string]any{testDigest: digestOf(cfg), testSize: len(cfg)},
				testLayersField:   descs,
			})

		case strings.Contains(r.URL.Path, "/blobs/"):
			// Counted under the lock: blob requests overlap now that layers are
			// fetched while the one before them unpacks (E641), and this
			// counter was written serially before they did.
			f.enterBlob()
			defer f.leaveBlob()

			for _, l := range f.layers {
				if strings.HasSuffix(r.URL.Path, digestOf(l)) {
					if f.corrupt {
						// One byte different: the digest no longer matches, and
						// nothing else about the response looks wrong.
						bad := append([]byte{}, l...)
						bad[len(bad)/2] ^= 0xff
						_, _ = w.Write(bad)

						return
					}

					_, _ = w.Write(l)

					return
				}
			}

			if strings.HasSuffix(r.URL.Path, digestOf(f.configBlob())) {
				_, _ = w.Write(f.configBlob())

				return
			}

			_, _ = w.Write([]byte("{}"))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	realm = srv.URL + "/token"

	return strings.TrimPrefix(srv.URL, "http://")
}

// The boundary between hash worlds. A blob is fetched by its SHA-256 descriptor
// and must hash to it before anything is done with the bytes.
//
// Verification happens *before* unpacking, not after: unpacking is where a
// hostile archive gets to create files, so checking afterwards would be checking
// after the damage.
func TestCorruptBlobIsRefused(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "hello")}, corrupt: true}
	host := reg.start(t)

	dir := t.TempDir()

	_, err := image.Pull(context.Background(), host+"/library/test:1", dir, image.Options{Plain: true})
	if err == nil {
		t.Fatal("a blob that did not match its digest was accepted")
	}

	if !strings.Contains(err.Error(), "sha256:") {
		t.Errorf("refusal does not name the expected digest:\n%s", err)
	}

	// And nothing may have been unpacked from it.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == "f" {
			t.Error("a file from a corrupt layer was unpacked")
		}
	}
}

// The happy path: layers arrive, verify, and unpack in order.
func TestPullUnpacksVerifiedLayers(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{
		gzipTar(t, "base", "one"),
		gzipTar(t, "top", "two"),
	}}
	host := reg.start(t)

	dir := t.TempDir()

	_, err := image.Pull(context.Background(), host+"/library/test:1", dir, image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{"base": "one", "top": "two"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)

			continue
		}

		if string(b) != want {
			t.Errorf("%s = %q, want %q", name, b, want)
		}
	}
}

// Reference parsing is tested in ref_test.go, against the canonical parser.
//
// The test that stood here checked the same inputs and expected
// `index.docker.io` as the registry - the API host rather than the canonical
// domain. Those two are deliberately separate now: Ref.Registry is the domain
// (`docker.io`), and registryHost maps it to the address that serves the API.

// indexRegistry serves a manifest list (multi-arch), as every real registry does
// for a popular base image.
type indexRegistry struct{ platforms []string }

func (ix *indexRegistry) start(t *testing.T) string {
	t.Helper()

	layer := gzipTar(t, "arch-file", "content")

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/1"):
			ms := make([]map[string]any, 0, len(ix.platforms))
			for _, p := range ix.platforms {
				wantOS, arch, _ := strings.Cut(p, "/")
				ms = append(ms, map[string]any{
					testMediaType: ocispec.MediaTypeImageManifest,
					testDigest:    digestOf([]byte(p)),
					testSize:      100,
					"platform":    map[string]any{"os": wantOS, "architecture": arch},
				})
			}

			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				testSchemaVersion: 2,
				testMediaType:     "application/vnd.oci.image.index.v1+json",
				"manifests":       ms,
			})

		case strings.Contains(r.URL.Path, "/manifests/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				testSchemaVersion: 2,
				testMediaType:     ocispec.MediaTypeImageManifest,
				testLayersField: []map[string]any{{
					testMediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
					testDigest:    digestOf(layer),
					testSize:      len(layer),
				}},
			})

		default:
			_, _ = w.Write(layer)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return strings.TrimPrefix(srv.URL, "http://")
}

// An index must be resolved to the manifest for the platform being built, and
// **refused** when that platform is absent.
//
// Picking any available manifest instead would produce a build that runs the
// wrong architecture's binaries - which fails as "exec format error" somewhere
// far from here, if it fails at all.
func TestMissingPlatformIsRefused(t *testing.T) {
	t.Parallel()

	host := (&indexRegistry{platforms: []string{"linux/s390x", "linux/riscv64"}}).start(t)

	_, err := image.Pull(context.Background(), host+"/library/test:1", t.TempDir(),
		image.Options{Plain: true, Platform: testPlatform})
	if err == nil {
		t.Fatal("an image with no matching platform was accepted")
	}

	for _, want := range []string{testPlatform, "linux/s390x"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q (wanted, and what is available):\n%s", want, err)
		}
	}
}

// And the right one is selected when present.
func TestPlatformIsSelectedFromAnIndex(t *testing.T) {
	t.Parallel()

	host := (&indexRegistry{platforms: []string{"linux/amd64", testPlatform}}).start(t)

	dir := t.TempDir()

	_, err := image.Pull(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true, Platform: testPlatform})
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.ReadFile(filepath.Join(dir, "arch-file"))
	if err != nil {
		t.Errorf("the selected manifest's layer was not unpacked: %v", err)
	}
}

// zstdTar is a one-file layer, zstd-compressed as a registry would serve it.
func zstdTar(t *testing.T, name, body string) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}

	tw := tar.NewWriter(zw)

	err = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []interface{ Close() error }{tw, zw} {
		err := c.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	return buf.Bytes()
}

// A zstd layer pulls, end to end.
//
// `decompress` has unit tests, and they would pass just as well if nothing ever
// called it with a zstd media type - a manifest gate refusing the layer earlier,
// or a caller that hard-codes gzip, and the support is written and unreachable.
// A grep is not the answer either: searching for `decompress(` while filtering
// out lines matching `compress` hid the one call site, because the caller's name
// contains the filter's word.
//
// So the claim is made where it is used: a registry that declares zstd, through
// `Pull`, to a file on disk with the right contents.
func TestAZstdLayerPullsEndToEnd(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{
		layers:    [][]byte{zstdTar(t, "compressed", "by zstd")},
		mediaType: "application/vnd.oci.image.layer.v1.tar+zstd",
	}
	host := reg.start(t)

	dir := t.TempDir()

	_, err := image.Pull(context.Background(), host+"/library/test:1", dir, image.Options{Plain: true})
	if err != nil {
		t.Fatalf("a registry serving a zstd layer could not be pulled: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "compressed"))
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "by zstd" {
		t.Errorf("the layer unpacked to %q", b)
	}
}

// enterBlob records a blob request arriving, and holds it long enough that a
// concurrent one has somewhere to overlap.
func (f *fakeRegistry) enterBlob() {
	f.blobMu.Lock()
	f.served++
	f.inFlight++

	if f.inFlight > f.mostBlobs {
		f.mostBlobs = f.inFlight
	}

	f.blobMu.Unlock()

	if f.blobDelay > 0 {
		time.Sleep(f.blobDelay)
	}
}

func (f *fakeRegistry) leaveBlob() {
	f.blobMu.Lock()
	f.inFlight--
	f.blobMu.Unlock()
}

// peakBlobs is the most blob requests that were ever in flight at once.
func (f *fakeRegistry) peakBlobs() int {
	f.blobMu.Lock()
	defer f.blobMu.Unlock()

	return f.mostBlobs
}
