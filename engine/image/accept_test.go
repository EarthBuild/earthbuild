package image_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// A registry that answers only what it was asked for.
//
// `Accept` is not decoration. A registry holding a multi-platform image decides
// from that header what to send: ask for a manifest and not an index and a
// strict one answers 406, a lenient one answers with something for the wrong
// platform, and an old one answers with a schema1 document this engine cannot
// read. Which of the three happens is the registry's choice, so the header is
// the only part of it this engine controls.
//
// Nothing tested it. The four media types in `get` were four strings that could
// be trimmed to three with every test in the package still passing, because
// `fakeRegistry` serves a manifest to anybody who asks - which is exactly the
// lenient case, and the one that hides the bug.
//
// The engine never reads `mediaType` off what comes back; it decides from the
// shape, an index being a document with `manifests` in it. That is robust in the
// right direction and it is also why the request side has to be asserted here:
// there is no later point at which asking for the wrong thing is noticed.
type strictRegistry struct {
	// accepted is what the last manifest request was willing to receive.
	accepted string
	layer    []byte
}

func (s *strictRegistry) start(t *testing.T) string {
	t.Helper()

	cfg := []byte("{}")
	mux := http.NewServeMux()

	// The manifest an index points at, addressed by its digest.
	manifest, err := json.Marshal(map[string]any{
		testSchemaVersion: 2,
		testMediaType:     ocispec.MediaTypeImageManifest,
		testConfigField:   map[string]any{testDigest: digestOf(cfg), testSize: len(cfg)},
		testLayersField: []map[string]any{{
			testMediaType: ocispec.MediaTypeImageLayerGzip,
			testDigest:    digestOf(s.layer),
			testSize:      len(s.layer),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			s.accepted = r.Header.Get("Accept")

			// Addressed by digest: the caller already chose, so serve it.
			if strings.Contains(r.URL.Path, "sha256:") {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
				_, _ = w.Write(manifest)

				return
			}

			// Addressed by tag, and this image is multi-platform. A strict
			// registry sends an index or it sends 406; it does not guess.
			if !strings.Contains(s.accepted, ocispec.MediaTypeImageIndex) {
				w.WriteHeader(http.StatusNotAcceptable)

				return
			}

			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			_ = json.NewEncoder(w).Encode(map[string]any{
				testSchemaVersion: 2,
				testMediaType:     ocispec.MediaTypeImageIndex,
				"manifests": []map[string]any{{
					testMediaType: ocispec.MediaTypeImageManifest,
					testDigest:    digestOf(manifest),
					testSize:      len(manifest),
					"platform": map[string]any{
						"os": runtime.GOOS, "architecture": runtime.GOARCH,
					},
				}},
			})

		case strings.HasSuffix(r.URL.Path, digestOf(s.layer)):
			_, _ = w.Write(s.layer)

		case strings.Contains(r.URL.Path, "/blobs/"):
			_, _ = w.Write(cfg)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return strings.TrimPrefix(srv.URL, "http://")
}

// A multi-platform image is pulled, which requires having asked for an index.
//
// Measured by removing `application/vnd.oci.image.index.v1+json` from the header
// this engine sends: `406 Not Acceptable`, and the pull fails with nothing to
// suggest the request was the problem (E201).
func TestAMultiPlatformImageIsAskedForAsAnIndex(t *testing.T) {
	t.Parallel()

	reg := &strictRegistry{layer: gzipTar(t, "f", "hello")}
	host := reg.start(t)
	dir := t.TempDir()

	_, err := image.Pull(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true})
	if err != nil {
		t.Fatalf("a multi-platform image did not pull: %v\n  asked for %q",
			err, reg.accepted)
	}

	_, err = os.Stat(filepath.Join(dir, "f"))
	if err != nil {
		t.Errorf("the selected manifest's layer was not unpacked: %v", err)
	}
}

// Every kind of manifest this engine can read, it asks for.
//
// The engine decides an index from the presence of `manifests` rather than from
// the declared type, so it reads Docker's manifest list as readily as OCI's. A
// header naming only the OCI pair would have a registry holding a Docker-format
// image answer 406 for one this engine could have handled perfectly well.
//
// Docker's two are literals: `github.com/docker/distribution` is not a
// dependency and would not be worth becoming one for two strings that a
// published specification has frozen. The OCI two come from the spec package,
// which is already a direct dependency (E201).
func TestTheAcceptHeaderNamesBothManifestFormats(t *testing.T) {
	t.Parallel()

	reg := &strictRegistry{layer: gzipTar(t, "f", "hello")}
	host := reg.start(t)

	_, _ = image.Pull(context.Background(), host+"/library/test:1", t.TempDir(),
		image.Options{Plain: true})

	for _, want := range []string{
		ocispec.MediaTypeImageManifest,
		ocispec.MediaTypeImageIndex,
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
	} {
		if !strings.Contains(reg.accepted, want) {
			t.Errorf("the engine does not ask for %s\n  asked for %q",
				want, reg.accepted)
		}
	}
}

// A 406 says the request was the problem, and what was asked for.
//
// `returned 406 Not Acceptable` is true and useless. Of the status codes a
// registry can answer with, this is the one with a single cause: nothing the
// client offered could be served. Everything else about the request was fine -
// the reference resolved, the token was accepted, the path existed - so a reader
// given only the status looks at the image, the credentials and the network
// before the header, which is the one thing that was actually wrong.
//
// Told apart from the general case deliberately. A 404 has several causes and a
// 500 has any number, and inventing a cause for those would be guessing dressed
// as help.
func TestANotAcceptableSaysWhatWasAskedFor(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotAcceptable)
		}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")

	_, err := image.Pull(context.Background(), host+"/library/test:1",
		t.TempDir(), image.Options{Plain: true})
	if err == nil {
		t.Fatal("a registry that refused every format was treated as a success")
	}

	msg := err.Error()

	if !strings.Contains(msg, ocispec.MediaTypeImageManifest) {
		t.Errorf("the refusal does not say what was asked for:\n  %s", msg)
	}

	// And it must still carry the status, which is what a reader searches for.
	if !strings.Contains(msg, "406") {
		t.Errorf("the refusal no longer names the status:\n  %s", msg)
	}
}
