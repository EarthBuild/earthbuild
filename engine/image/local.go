package image

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// localBlobs is the directory under Options.Local holding every blob SAVE IMAGE
// wrote, by digest. A reference cannot begin with a dot, so no LayoutName can
// collide with it.
const localBlobs = ".blobs"

// LayoutName is the directory SAVE IMAGE writes a reference's layout to,
// under Options.Local: a reference holds slashes and colons a directory name
// cannot.
func LayoutName(ref string) string {
	out := []rune(ref)
	for i, r := range out {
		if r == '/' || r == ':' || r == os.PathSeparator {
			out[i] = '_'
		}
	}

	return string(out)
}

// SaveLocal files a layout SAVE IMAGE wrote under root by digest, so that a
// reference pinned to it can be pulled with no registry, and reports the
// digest to pin to.
//
// **Only a pinned reference is served from here.** A digest names its bytes, so
// a local copy is as right as a registry's - and verified the same way. A tag
// moves, so it is always asked of its registry; see tagHint for what happens
// when the registry does not have it.
//
// Linked rather than copied where the filesystem allows: the blobs are the
// image, and a second copy of every layer would double what SAVE IMAGE costs
// on disk.
func SaveLocal(layout, root string) (string, error) {
	desc, _, err := manifestOfLayout(layout)
	if err != nil {
		return "", err
	}

	from := filepath.Join(layout, "blobs")

	err = filepath.WalkDir(from, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}

		rel, relErr := filepath.Rel(from, p)
		if relErr != nil {
			return relErr
		}

		to := filepath.Join(root, localBlobs, rel)
		if _, statErr := os.Lstat(to); statErr == nil {
			return nil // content-addressed: whoever filed it first filed the same bytes
		}

		mkErr := os.MkdirAll(filepath.Dir(to), 0o750)
		if mkErr != nil {
			return mkErr
		}

		if os.Link(p, to) == nil {
			return nil
		}

		return copyBlobFile(p, to)
	})
	if err != nil {
		return "", fmt.Errorf("file %s in the local image store: %w", layout, err)
	}

	return string(desc.Digest), nil
}

func copyBlobFile(from, to string) error {
	b, err := os.ReadFile(from) //nolint:gosec // a layout this engine wrote
	if err != nil {
		return err
	}

	tmp := to + ".partial"

	err = os.WriteFile(tmp, b, 0o600)
	if err != nil {
		return err
	}

	return os.Rename(tmp, to)
}

// localBlobPath is where root keeps a digest, or "" for one it cannot name.
func localBlobPath(root, digest string) string {
	alg, hex, ok := strings.Cut(digest, ":")
	if !ok || alg == "" || hex == "" || strings.ContainsAny(digest, `/\.`) {
		return ""
	}

	return filepath.Join(root, localBlobs, alg, hex)
}

// localManifest is a pinned reference's manifest from root, verified, or nil.
func localManifest(root, digest string) []byte {
	at := localBlobPath(root, digest)
	if root == "" || at == "" {
		return nil
	}

	body, err := os.ReadFile(at) //nolint:gosec // a path derived from a digest
	if err != nil || verify(body, digest) != nil {
		return nil
	}

	return body
}

// localTransport answers a registry's blob and manifest requests from root.
//
// A transport rather than a second pull path, so the layers and the
// configuration are fetched, verified and unpacked by exactly the code that
// fetches them from a registry: the store is one more place bytes come from,
// and nothing downstream can tell - or needs to.
type localTransport struct{ root string }

func (l localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	digest := path.Base(r.URL.Path)
	kind := path.Base(path.Dir(r.URL.Path))

	at := localBlobPath(l.root, digest)
	if at == "" || (kind != "blobs" && kind != "manifests") {
		return localAnswer(r, http.StatusNotFound, nil, ""), nil
	}

	body, err := os.ReadFile(at) //nolint:gosec // a path derived from a digest
	if errors.Is(err, os.ErrNotExist) {
		return localAnswer(r, http.StatusNotFound, nil, ""), nil
	}

	if err != nil {
		return nil, err
	}

	mediaType := ""

	if kind == "manifests" {
		var m struct {
			MediaType string `json:"mediaType"`
		}

		_ = json.Unmarshal(body, &m)
		mediaType = m.MediaType
	}

	return localAnswer(r, http.StatusOK, body, mediaType), nil
}

func localAnswer(r *http.Request, status int, body []byte, mediaType string) *http.Response {
	h := http.Header{}
	if mediaType != "" {
		h.Set("Content-Type", mediaType)
	}

	var rd io.Reader = bytes.NewReader(body)
	if r.Method == http.MethodHead {
		rd = bytes.NewReader(nil)
	}

	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Header: h,
		Body: io.NopCloser(rd), ContentLength: int64(len(body)), Request: r,
	}
}

// tagHint explains a tag the registry would not give, when this machine saved
// an image under that name: tags are resolved remotely because they move, and
// the digest SAVE IMAGE wrote is the way to use the local one.
func tagHint(ref string, opt Options, err error) error {
	if opt.Local == "" {
		return err
	}

	layout := filepath.Join(opt.Local, LayoutName(ref))

	desc, _, lerr := manifestOfLayout(layout)
	if lerr != nil {
		return err
	}

	when := ""
	if fi, serr := os.Stat(filepath.Join(layout, "index.json")); serr == nil {
		when = " at " + fi.ModTime().Format(time.DateTime)
	}

	return fmt.Errorf("%w\n  this machine saved %s with SAVE IMAGE%s, but a tag is always"+
		" resolved by its registry, because tags move"+
		"\n  to use the saved image, pin it by digest: %s@%s",
		err, ref, when, Untagged(ref), desc.Digest)
}

// Untagged is a reference without its tag, as written: what `@<digest>` is
// appended to when pinning one.
func Untagged(ref string) string {
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon]
	}

	return ref
}
