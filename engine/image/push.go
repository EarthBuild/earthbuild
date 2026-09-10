package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/EarthBuild/earthbuild/engine/timing"
)

// PushOptions is what a push needs beyond the layout and the name.
type PushOptions struct {
	// Client is the one the pull path uses, so a push shares its transport,
	// timeouts and proxy settings rather than growing its own.
	Client *http.Client
	// Challenges is where a registry's token endpoint is remembered between
	// builds, as it is for pulls. Empty disables the memory, not the auth.
	Challenges string
	// Plain talks HTTP rather than HTTPS. For a registry on this machine, and
	// for the tests here; a remote registry over plain HTTP would hand the
	// credential to anything on the path.
	Plain bool
}

// Push uploads an OCI layout to the registry its reference names, and reports
// the digest the manifest landed under.
//
// **`SAVE IMAGE --push` used to parse and do nothing.** The build succeeded and
// a note said the image had not been published, which is honest and is not what
// anyone writing `--push` was asking for.
//
// Blobs first and the manifest last, which is the order the registry API
// requires and is also the safe one: a manifest naming a blob that is not there
// is a reference to a broken image, and the window for that is the whole of the
// upload if it goes the other way round.
func Push(ctx context.Context, layout, ref string, opt PushOptions) (string, error) {
	defer timing.Phase("registry:push", ref)()

	r, err := ParseRef(ref)
	if err != nil {
		return "", fmt.Errorf("push %s: %w", ref, err)
	}

	desc, raw, err := manifestOfLayout(layout)
	if err != nil {
		return "", fmt.Errorf("push %s: %w", ref, err)
	}

	var m ocispec.Manifest

	err = json.Unmarshal(raw, &m)
	if err != nil {
		return "", fmt.Errorf("push %s: read its manifest: %w", ref, err)
	}

	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}

	p := &pusher{
		client: client,
		base:   fmt.Sprintf("%s://%s/v2/%s", schemeFor(opt.Plain), r.Registry, r.Repository),
		opt:    opt,
	}

	// The config is a blob like any other, and is listed apart from the layers
	// only because a manifest names it apart.
	for _, d := range append([]ocispec.Descriptor{m.Config}, m.Layers...) {
		err = p.pushBlob(ctx, layout, string(d.Digest))
		if err != nil {
			return "", fmt.Errorf("push %s: %w", ref, err)
		}
	}

	at := r.Tag
	if at == "" {
		at = "latest"
	}

	err = p.putManifest(ctx, at, desc.MediaType, raw)
	if err != nil {
		return "", fmt.Errorf("push %s: %w", ref, err)
	}

	return string(desc.Digest), nil
}

func schemeFor(plain bool) string {
	if plain {
		return "http"
	}

	return "https"
}

// manifestOfLayout reads which manifest a layout holds, and its bytes.
//
// The bytes rather than a re-encoding: a manifest is named by the digest of
// exactly the bytes on disk, so anything that round-tripped it through a struct
// would push it under a name that is not its own.
func manifestOfLayout(dir string) (ocispec.Descriptor, []byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "index.json")) //nolint:gosec // a layout this engine wrote
	if err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("read the layout index: %w", err)
	}

	var index ocispec.Index

	err = json.Unmarshal(raw, &index)
	if err != nil {
		return ocispec.Descriptor{}, nil, fmt.Errorf("read the layout index: %w", err)
	}

	if len(index.Manifests) == 0 {
		return ocispec.Descriptor{}, nil, fmt.Errorf("the layout at %s names no image", dir)
	}

	desc := index.Manifests[0]

	body, err := blobBytes(dir, string(desc.Digest))
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	return desc, body, nil
}

func blobBytes(dir, digest string) ([]byte, error) {
	at := filepath.Join(dir, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))

	body, err := os.ReadFile(at) //nolint:gosec // a layout this engine wrote
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", digest, err)
	}

	return body, nil
}

// pushBlob uploads one blob, unless the registry already has it.
//
// **Asked before sent**, because a base layer is most of an image and most
// pushes to one repository share it. The check is a HEAD, which costs a round
// trip against an upload that costs the layer.
func (p *pusher) pushBlob(ctx context.Context, layout, digest string) error {
	has, err := p.blobPresent(ctx, digest)
	if err != nil {
		return err
	}

	if has {
		return nil
	}

	body, err := blobBytes(layout, digest)
	if err != nil {
		return err
	}

	at, err := p.startUpload(ctx)
	if err != nil {
		return fmt.Errorf("begin uploading %s: %w", digest, err)
	}

	// The digest goes on the URL the registry handed back, which may already
	// carry a query of its own - a registry is free to put state there and
	// several do.
	sep := "?"
	if strings.Contains(at, "?") {
		sep = "&"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		at+sep+"digest="+digest, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("upload %s: %w", digest, err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(body))

	return p.expectStatus(ctx, req, "upload "+digest,
		http.StatusCreated, http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (p *pusher) blobPresent(ctx context.Context, digest string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, p.base+"/blobs/"+digest, nil)
	if err != nil {
		return false, fmt.Errorf("ask about %s: %w", digest, err)
	}

	resp, err := p.do(ctx, req)
	if err != nil {
		return false, fmt.Errorf("ask about %s: %w", digest, err)
	}

	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	return resp.StatusCode == http.StatusOK, nil
}

func (p *pusher) startUpload(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/blobs/uploads/", nil)
	if err != nil {
		return "", err
	}

	resp, err := p.do(ctx, req)
	if err != nil {
		return "", err
	}

	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("the registry answered %s", resp.Status)
	}

	at := resp.Header.Get("Location")
	if at == "" {
		return "", errors.New("the registry accepted an upload and said nowhere to send it")
	}

	// A relative Location is allowed and several registries use one.
	if strings.HasPrefix(at, "/") {
		cut := strings.Index(p.base, "/v2/")

		return p.base[:cut] + at, nil
	}

	return at, nil
}

func (p *pusher) putManifest(ctx context.Context, at, media string, raw []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		p.base+"/manifests/"+at, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("name the image %s: %w", at, err)
	}

	if media == "" {
		media = ocispec.MediaTypeImageManifest
	}

	req.Header.Set("Content-Type", media)
	req.ContentLength = int64(len(raw))

	return p.expectStatus(ctx, req, "name the image "+at,
		http.StatusCreated, http.StatusOK, http.StatusAccepted)
}

func authorise(req *http.Request, tok string) {
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// expectStatus runs a request and turns anything unexpected into a message that
// names what failed and what the registry said about it.
func (p *pusher) expectStatus(ctx context.Context, req *http.Request, what string, ok ...int) error {
	resp, err := p.do(ctx, req)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if slices.Contains(ok, resp.StatusCode) {
		_, _ = io.Copy(io.Discard, resp.Body)

		return nil
	}

	// The body, because a registry's refusal is in it and not in the status: a
	// bare "403 Forbidden" is the difference between a wrong credential and a
	// repository that does not exist, and it says neither.
	said, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))

	return fmt.Errorf("%s: the registry answered %s\n  %s",
		what, resp.Status, strings.TrimSpace(string(said)))
}

// pusher is the request layer a push runs on: one connection's worth of state,
// and a token it learns rather than asks for up front.
type pusher struct {
	client *http.Client
	base   string
	opt    PushOptions
	tok    string
}

// do runs a request, and on a challenge learns the token and runs it once more.
//
// **The challenge is drawn by the real request, never by a probe.** A registry
// issues the scope it was asked for, so a probe that reads yields
// `repository:app:pull` and every upload made with it comes back 401 - which
// reads as a bad credential and is a wrong scope. Probing with a *write* draws
// the right scope and leaves an upload session the registry now has to clean up.
// Letting the operation itself be the probe has neither problem.
func (p *pusher) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	authorise(req, p.tok)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	at, err := tokenEndpoint(challenge)
	if err != nil {
		return nil, err
	}

	p.tok, err = fetchTokenAs(ctx, p.client, at, credentialForURL(p.base))
	if err != nil {
		return nil, err
	}

	if p.opt.Challenges != "" {
		rememberChallenge(p.opt.Challenges, p.base, at)
	}

	again, err := replay(ctx, req)
	if err != nil {
		return nil, err
	}

	authorise(again, p.tok)

	return p.client.Do(again)
}

// replay rebuilds a request so it can be sent a second time.
//
// A body is a reader and a sent request has consumed it. `http.NewRequest`
// leaves `GetBody` on anything it can rewind, which is every body this package
// sends, and a request with no body needs nothing.
func replay(ctx context.Context, req *http.Request) (*http.Request, error) {
	var body io.Reader

	if req.GetBody != nil {
		rewound, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("send %s again: %w", req.URL, err)
		}

		body = rewound
	}

	// The URL is the one already sent, not a caller's: this replays a request
	// this package built and the registry answered.
	//nolint:gosec // the request's own URL, replayed
	again, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), body)
	if err != nil {
		return nil, err
	}

	maps.Copy(again.Header, req.Header)

	again.ContentLength = req.ContentLength

	return again, nil
}
