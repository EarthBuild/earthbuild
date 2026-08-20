package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/distribution/reference"
)

// Ref is a parsed image reference.
type Ref struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string // when the reference pins one, which is the only form that is reproducible
}

// ParseRef splits a reference into its parts.
//
// Uses `distribution/reference`, which is the canonical parser, is already a
// dependency, and is what the BuildKit path in this repository uses. The
// hand-rolled version it replaces got digest-only references wrong - it set both
// a digest *and* a `latest` tag, so a pull pinned to a digest also carried a tag
// that contradicted it - and would have gone on being wrong in ways nobody finds
// until a reference in the wild uses the shape it mishandles.
func ParseRef(s string) (Ref, error) {
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(s))
	if err != nil {
		return Ref{}, fmt.Errorf("parse image reference %q: %w", s, err)
	}

	r := Ref{
		Registry:   reference.Domain(named),
		Repository: reference.Path(named),
	}

	if tagged, ok := named.(reference.Tagged); ok {
		r.Tag = tagged.Tag()
	}

	if digested, ok := named.(reference.Digested); ok {
		r.Digest = digested.Digest().String()
	}

	// Neither a tag nor a digest means `latest`, which is what
	// reference.TagNameOnly encodes and what every other tool assumes.
	if r.Tag == "" && r.Digest == "" {
		r.Tag = "latest"
	}

	return r, nil
}

// registryHost is the host to talk to for a domain.
//
// `docker.io` is the canonical *name* of the default registry and not a host
// that serves the API; the requests go to registry-1.docker.io. Keeping the two
// apart is why Ref.Registry holds the domain rather than an address.
func registryHost(domain string) string {
	if domain == "docker.io" {
		return "registry-1.docker.io"
	}

	return domain
}

// Options configure a pull.
type Options struct {
	// Plain uses http rather than https. For a local registry and for tests;
	// never a default, because a plaintext pull is one an intermediary can
	// rewrite.
	Plain bool
	// Client is the HTTP client. A default one is used when nil.
	Client *http.Client
	// Platform is "os/arch". Defaults to this machine's.
	Platform string
}

// maxManifest bounds a manifest document. A registry that returns an unbounded
// one must not be able to exhaust the engine's memory before anything is even
// verified.
const maxManifest = 8 << 20

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type manifest struct {
	MediaType string       `json:"mediaType"`
	Config    descriptor   `json:"config"`
	Layers    []descriptor `json:"layers"`
	Manifests []indexEntry `json:"manifests"`
}

type indexEntry struct {
	descriptor

	Platform struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

func (e indexEntry) platform() string { return e.Platform.OS + "/" + e.Platform.Architecture }

// selectPlatform resolves an index to the manifest for one platform.
//
// Refuses rather than falling back to the first entry. A build that quietly used
// another architecture's manifest fails as "exec format error" somewhere far from
// here, if it fails at all - and on a multi-arch fleet it might not fail until a
// different worker runs it.
func selectPlatform(m manifest, want string) (string, error) {
	available := make([]string, 0, len(m.Manifests))

	for _, e := range m.Manifests {
		if e.platform() == want {
			return e.Digest, nil
		}

		available = append(available, e.platform())
	}

	return "", fmt.Errorf("no manifest for %s\n  this image provides: %s",
		want, strings.Join(available, ", "))
}

// Pull fetches an image and unpacks its layers into dir, in order.
//
// Every blob is verified against its descriptor digest **before** its contents
// are used. Verifying afterwards would mean unpacking hostile bytes first, and
// unpacking is exactly where an archive gets to create files.
func Pull(ctx context.Context, ref, dir string, opt Options) (ocispec.ImageConfig, error) {
	r, err := ParseRef(ref)
	if err != nil {
		return ocispec.ImageConfig{}, err
	}

	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}

	scheme := "https"
	if opt.Plain {
		scheme = "http"
	}

	base := fmt.Sprintf("%s://%s/v2/%s", scheme, registryHost(r.Registry), r.Repository)

	target := r.Tag
	if r.Digest != "" {
		target = r.Digest
	}

	// Registries answer an anonymous request with 401 and a challenge naming
	// where to get a token. Public images need this too, so it is not an
	// authentication feature - it is how a pull works at all.
	tok, err := token(ctx, client, base+"/manifests/"+target)
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("authenticate to %s: %w", r.Registry, err)
	}

	body, err := get(ctx, client, tok, base+"/manifests/"+target, maxManifest)
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("fetch the manifest for %s: %w", ref, err)
	}

	var m manifest
	err = json.Unmarshal(body, &m)
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("parse the manifest for %s: %w", ref, err)
	}

	// A manifest list resolves to one manifest before anything is fetched.
	if len(m.Manifests) > 0 {
		want := opt.Platform
		if want == "" {
			want = runtime.GOOS + "/" + runtime.GOARCH
		}

		digest, err := selectPlatform(m, want)
		if err != nil {
			return ocispec.ImageConfig{}, fmt.Errorf("%s: %w", ref, err)
		}

		body, err = get(ctx, client, tok, base+"/manifests/"+digest, maxManifest)
		if err != nil {
			return ocispec.ImageConfig{}, fmt.Errorf("fetch the %s manifest for %s: %w", want, ref, err)
		}

		m = manifest{}
		err = json.Unmarshal(body, &m)
		if err != nil {
			return ocispec.ImageConfig{}, fmt.Errorf("parse the %s manifest for %s: %w", want, ref, err)
		}
	}

	if len(m.Layers) == 0 {
		return ocispec.ImageConfig{}, fmt.Errorf("%s has no layers", ref)
	}

	// 0750 for the directory this engine owns; the image's own entries get the
	// modes the archive declares, applied once they are all in place.
	err = os.MkdirAll(dir, 0o750)
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("create the unpack directory: %w", err)
	}

	// Ordered, oldest first: a later layer's whiteout must be applied after the
	// file it deletes has been unpacked, or the deletion is a no-op.
	for i, d := range m.Layers {
		err := pullLayer(ctx, client, tok, base, d, dir)
		if err != nil {
			return ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, err)
		}
	}

	// The configuration is what an image *declares*: ENTRYPOINT, ENV, WORKDIR,
	// USER. Fetched after the layers, because a manifest whose layers cannot be
	// pulled has nothing worth configuring - and dropped if it is absent, since
	// an image that declares nothing is ordinary and its manifest may not name
	// a config at all.
	cfg, err := pullConfig(ctx, client, tok, base, m.Config, opt.Platform)
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("configuration of %s: %w", ref, err)
	}

	return cfg, nil
}

// pullConfig fetches and verifies an image's configuration blob.
func pullConfig(
	ctx context.Context, client *http.Client, tok, base string, d descriptor, want string,
) (ocispec.ImageConfig, error) {
	if d.Digest == "" {
		return ocispec.ImageConfig{}, nil
	}

	limit := d.Size
	if limit <= 0 {
		limit = maxManifest
	}

	blob, err := get(ctx, client, tok, base+"/blobs/"+d.Digest, limit)
	if err != nil {
		return ocispec.ImageConfig{}, err
	}

	// Verified like any other blob: the configuration decides what a container
	// runs, so a substituted one chooses the command.
	err = verify(blob, d.Digest)
	if err != nil {
		return ocispec.ImageConfig{}, err
	}

	var img struct {
		Architecture string              `json:"architecture"`
		OS           string              `json:"os"`
		Config       ocispec.ImageConfig `json:"config"`
	}

	err = json.Unmarshal(blob, &img)
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("parse the configuration: %w", err)
	}

	return img.Config, checkArchitecture(img.OS, img.Architecture, want)
}

func pullLayer(ctx context.Context, client *http.Client, tok, base string, d descriptor, dir string) error {
	limit := d.Size
	if limit <= 0 {
		limit = 1 << 30
	}

	// Read the whole blob and verify before unpacking. Streaming into the
	// unpacker while hashing would be faster and would also mean the archive has
	// already written files by the time the digest is found to be wrong.
	blob, err := get(ctx, client, tok, base+"/blobs/"+d.Digest, limit)
	if err != nil {
		return err
	}

	err = verify(blob, d.Digest)
	if err != nil {
		return err
	}

	r, err := decompress(blob, d.MediaType)
	if err != nil {
		return err
	}

	defer r.Close()

	return Unpack(r, dir)
}

// verify checks a blob against its descriptor digest.
//
// This is the boundary between the two hash worlds: SHA-256 is what a registry
// speaks and is confined to exactly this check, while ℋ is what identifies a
// layer inside the engine (green paper §3.1).
func verify(blob []byte, want string) error {
	algo, hexsum, ok := strings.Cut(want, ":")
	if !ok {
		return fmt.Errorf("malformed digest %q", want)
	}

	if algo != "sha256" {
		return fmt.Errorf("unsupported digest algorithm %q; registries are read as SHA-256", algo)
	}

	sum := sha256.Sum256(blob)
	if got := hex.EncodeToString(sum[:]); got != hexsum {
		return fmt.Errorf(
			"blob does not match its digest\n  expected %s\n  received sha256:%s\n"+
				"  the registry or something between it and here returned different bytes",
			want, got)
	}

	return nil
}

// token performs the registry's bearer-token dance: an anonymous request draws
// a 401 with a challenge, which names a realm and scope to fetch a token from.
//
// Returns the empty string when the registry does not challenge, which is the
// case for a local one, and is not an error.
func token(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build the challenge request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("probe %s: %w", url, err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") {
		return "", fmt.Errorf("unsupported authentication challenge %q", challenge)
	}

	var realm, service, scope string

	for part := range strings.SplitSeq(strings.TrimPrefix(challenge, "Bearer "), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}

		switch v = strings.Trim(v, `"`); k {
		case "realm":
			realm = v
		case "service":
			service = v
		case "scope":
			scope = v
		}
	}

	if realm == "" {
		return "", fmt.Errorf("authentication challenge names no realm: %q", challenge)
	}

	body, err := get(ctx, client, "", fmt.Sprintf("%s?service=%s&scope=%s", realm, service, scope), maxManifest)
	if err != nil {
		return "", err
	}

	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}

	err = json.Unmarshal(body, &t)
	if err != nil {
		return "", fmt.Errorf("parse the token response: %w", err)
	}

	if t.Token != "" {
		return t.Token, nil
	}

	return t.AccessToken, nil
}

// accepts is every manifest format this engine can read.
//
// A registry decides from this what to send, so a format missing here is one it
// answers 406 for - or, on a lenient registry, one it substitutes something else
// for. Both are the request's fault and neither says so at the point it happens.
//
// The engine reads a manifest by its shape rather than its declared type - an
// index is a document with `manifests` in it - which is why Docker's formats are
// here beside OCI's despite nothing in the parser naming them.
//
// OCI's two come from the specification package. Docker's two are literals:
// `github.com/docker/distribution` is not a dependency and is not worth becoming
// one for two strings a published specification has frozen (E201).
var accepts = []string{
	ocispec.MediaTypeImageManifest,
	"application/vnd.docker.distribution.manifest.v2+json",
	ocispec.MediaTypeImageIndex,
	"application/vnd.docker.distribution.manifest.list.v2+json",
}

func get(ctx context.Context, client *http.Client, tok, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	req.Header.Set("Accept", strings.Join(accepts, ", "))

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", url, err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 406 is the one status here with a single cause: nothing this engine
		// offered could be served. Every other part of the request worked, so a
		// reader given only the status goes looking at the reference, the
		// credentials and the network before the header - the one thing that was
		// actually wrong. Named for that reason and not for the others: a 404
		// has several causes and inventing one would be guessing dressed as
		// help.
		if resp.StatusCode == http.StatusNotAcceptable {
			return nil, fmt.Errorf(
				"%s returned %s\n  it has none of the formats this engine"+
					" reads: %s\n  the image may be in a format this engine"+
					" does not support yet",
				url, resp.Status, strings.Join(accepts, ", "))
		}

		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}

	// Bounded by the declared size, so a descriptor claiming a kilobyte cannot
	// stream a gigabyte into memory.
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}

	return b, nil
}

// checkArchitecture refuses an image built for a different machine.
//
// A multi-architecture image is an index and the right manifest is chosen from
// it; a *single*-manifest image has nothing to choose from, so nothing checked
// it at all. `hashicorp/terraform` and `namely/protoc-all` are linux/amd64 only,
// were pulled onto an arm64 machine, and failed inside the sandbox with
// `fork/exec /bin/sh: exec format error` - a message with nothing in it to
// connect to an image, an Earthfile or a platform.
//
// An image that says nothing about itself is trusted: that is old or unusual
// rather than wrong, and refusing it would refuse something that works.
func checkArchitecture(os, arch, want string) error {
	if os == "" || arch == "" || want == "" {
		return nil
	}

	has := os + "/" + arch
	if has == want {
		return nil
	}

	// Deliberately does *not* suggest building for the image's platform: on a
	// machine that cannot execute it, that only moves the failure from the pull
	// to the first RUN - which was the advice this message gave until somebody
	// followed it.
	return fmt.Errorf(
		"this image is %s and the build is for %s"+
			"\n  it is a single-manifest image, so there is no other platform to fetch"+
			"\n  use an image that provides %s, or run this build on a %s machine",
		has, want, want, has)
}
