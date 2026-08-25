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
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/distribution/reference"

	"github.com/EarthBuild/earthbuild/engine/timing"
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
// The two schemes a registry is reached over, named once.
//
// Three occurrences each across this package, and goconst is right for a reason
// that is not tidiness: `http` and `https` differ by one character, and a
// registry reached over the wrong one is a credential sent in clear. A typo in
// one of three copies would read as a deliberate choice.
const (
	schemeHTTPS = "https"
	schemePlain = "http"
)

// ParseRef reads an image reference the way a registry client does.
//
// Normalised, not merely split: `alpine` is `docker.io/library/alpine:latest`
// and `ubuntu:24.04` is `docker.io/library/ubuntu:24.04`, so two spellings of
// one image cannot become two cache keys. A digest survives if the reference
// carries one, because a pinned reference is the whole point of pinning.
//
// Refuses rather than guessing: a reference this cannot read is one the build
// asked for and would otherwise be silently replaced by something else.
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
	// Challenges is a directory in which to remember where each registry issues
	// tokens, so the round trip that collects that answer is paid once rather
	// than once per build. Empty means do not remember, which is what every
	// test and every caller without a cache directory wants.
	//
	// The token itself is never written here. See challenge.go.
	Challenges string
	// Stream unpacks each layer as it arrives rather than after it has landed.
	// Only meaningful with the layers kept apart - see streamLayerApart - and
	// ignored by Pull, whose merged unpack E647 measured at no gain.
	Stream bool
	// Retain, when set, is asked where to put each layer's compressed bytes as
	// they arrive, and the writer is closed when the layer is complete.
	//
	// **A blob is 61MB where its tree is 228MB and 15034 files**, and a layer
	// kept as a blob can still be named (E656) and served in part (E657) - at
	// 76% of an unpack-and-name (E658). None of that is reachable if the pull
	// throws the bytes away.
	//
	// A writer per layer rather than a buffer, because the streaming path never
	// holds a whole one; and the caller's rather than this package's, because
	// `ir` imports this package and so this package cannot name `engine/blob`.
	//
	// Best effort: a retention that fails leaves a pull that worked, since the
	// layer is unpacked either way.
	Retain func(layerDigest string) (io.WriteCloser, error)
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
// prepared is everything a pull needs once the manifest is in hand.
type prepared struct {
	client *http.Client
	m      manifest
	tok    string
	base   string
}

// prepare resolves a reference and fetches its manifest.
//
// Shared by Pull and PullApart, which differ only in where the layers land:
// one directory between them, or one each.
func prepare(ctx context.Context, ref string, opt Options) (prepared, error) {
	r, err := ParseRef(ref)
	if err != nil {
		return prepared{}, err
	}

	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}

	scheme := schemeHTTPS
	if opt.Plain {
		scheme = schemePlain
	}

	base := fmt.Sprintf("%s://%s/v2/%s", scheme, registryHost(r.Registry), r.Repository)

	target := r.Tag
	if r.Digest != "" {
		target = r.Digest
	}

	// Registries answer an anonymous request with 401 and a challenge naming
	// where to get a token. Public images need this too, so it is not an
	// authentication feature - it is how a pull works at all.
	tok, err := token(ctx, client, base+"/manifests/"+target, opt.Challenges, challengeKey(r))
	if err != nil {
		return prepared{}, fmt.Errorf("authenticate to %s: %w", r.Registry, err)
	}

	endManifest := timing.Phase("registry:manifest", target)
	body, err := get(ctx, client, tok, base+"/manifests/"+target, maxManifest)

	endManifest()
	if err != nil {
		return prepared{}, fmt.Errorf("fetch the manifest for %s: %w", ref, err)
	}

	var m manifest
	err = json.Unmarshal(body, &m)
	if err != nil {
		return prepared{}, fmt.Errorf("parse the manifest for %s: %w", ref, err)
	}

	// A manifest list resolves to one manifest before anything is fetched.
	if len(m.Manifests) > 0 {
		want := opt.Platform
		if want == "" {
			want = runtime.GOOS + "/" + runtime.GOARCH
		}

		digest, selectErr := selectPlatform(m, want)
		if selectErr != nil {
			return prepared{}, fmt.Errorf("%s: %w", ref, selectErr)
		}

		body, selectErr = get(ctx, client, tok, base+"/manifests/"+digest, maxManifest)
		if selectErr != nil {
			return prepared{}, fmt.Errorf("fetch the %s manifest for %s: %w", want, ref, selectErr)
		}

		m = manifest{}
		selectErr = json.Unmarshal(body, &m)
		if selectErr != nil {
			return prepared{}, fmt.Errorf("parse the %s manifest for %s: %w", want, ref, selectErr)
		}
	}

	if len(m.Layers) == 0 {
		return prepared{}, fmt.Errorf("%s has no layers", ref)
	}

	// 0750 for the directory this engine owns; the image's own entries get the
	// modes the archive declares, applied once they are all in place.
	return prepared{client: client, tok: tok, base: base, m: m}, nil
}

// Pull fetches an image and unpacks every layer into one directory.
func Pull(ctx context.Context, ref, dir string, opt Options) (ocispec.ImageConfig, error) {
	p, err := prepare(ctx, ref, opt)
	if err != nil {
		return ocispec.ImageConfig{}, err
	}

	client, tok, base, m := p.client, p.tok, p.base, p.m

	err = os.MkdirAll(dir, 0o750)
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("create the unpack directory: %w", err)
	}

	// **Fetched while the one before is unpacked; unpacked in order.**
	//
	// Ordered, oldest first: a later layer's whiteout must be applied after the
	// file it deletes has been unpacked, or the deletion is a no-op. That is
	// true of *unpacking* and of nothing else - a layer is an independent
	// object, and nothing about fetching one depends on another having arrived.
	//
	// Serially, the sum was the whole: `golang:1.26-alpine` spent 1.697s
	// fetching and 3.838s unpacking for a pull of 5.934s, so every byte of
	// waiting was time in which nothing was unpacked (E641).
	fetching := newLayerFetch(ctx, client, tok, base, m.Layers)

	for i, d := range m.Layers {
		got := fetching.await(i)
		if got.err != nil {
			return ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, got.err)
		}

		unpackErr := unpackLayer(got.blob, d, dir)
		if unpackErr != nil {
			return ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, unpackErr)
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

// layerBlob is a fetched layer, or the reason it could not be.
type layerBlob struct {
	err  error
	blob []byte
}

// layerBudget is how many bytes of un-unpacked layer a pull may hold.
//
// **Bytes rather than a count**, because a count is the wrong unit for the
// thing being bounded. A blob stays in memory until it is unpacked, so the risk
// is a pull holding some fraction of a large image; and a count that is safe
// for `golang:1.26-alpine` (five layers, ~100 MB) is not safe for an image with
// a two-gigabyte layer in it.
//
// It is also the wrong unit for the *gain*. Two layers ahead was enough to keep
// one fetch running during each unpack, and bought nothing measurable: the
// layer that dominates a language image is usually its last, so starting it one
// layer early leaves it nothing to overlap with. Reaching further ahead is what
// helps, and how much further should depend on how big the layers are.
//
// Measured on `golang:1.26-alpine`: serial 5.93s, two layers ahead 5.5-6.6s
// (noise), a budget that reaches all five 5.08s - and repeatable to 0.01s
// (E641).
// The layer the consumer is waiting for always starts, however big it is, so a
// single layer larger than the whole allowance cannot stall a pull - that is
// what the `j > i` in the loop below is for.
const layerBudget = 256 << 20

// fetchLayers starts fetching the next few layers and hands back the one asked
// for, in the order the caller must unpack them.
//
// **Started by the consumer, never ahead of it.** An earlier attempt gave each
// layer a goroutine that took a slot from a semaphore, and deadlocked: the
// goroutines race for slots in whatever order the scheduler likes, so layers 1
// and 2 could hold both while blocking to hand their blobs over - and layer 0,
// which the unpacking loop is waiting for, could never start. Fetching only
// what the consumer has reached, plus a fixed window ahead of it, cannot
// invert that way.
//
// Each channel holds one value, so a fetch never blocks on delivery, and the
// bytes in flight are bounded by layerBudget: a fetch starts only while the
// outstanding layers fit in it, and a blob is dropped as soon as it is
// unpacked.
type layerFetch struct {
	inflight map[int]chan layerBlob
	ctx      context.Context //nolint:containedctx // one pull's lifetime, see await
	client   *http.Client
	tok      string
	base     string
	layers   []descriptor
}

func newLayerFetch(ctx context.Context, client *http.Client, tok, base string,
	layers []descriptor,
) *layerFetch {
	return &layerFetch{
		inflight: map[int]chan layerBlob{}, ctx: ctx,
		client: client, tok: tok, base: base, layers: layers,
	}
}

// await is layer i's blob, having started it and the window after it.
func (f *layerFetch) await(i int) layerBlob {
	outstanding := int64(0)

	for j := i; j < len(f.layers); j++ {
		if _, going := f.inflight[j]; going {
			outstanding += f.layers[j].Size

			continue
		}

		if j > i && outstanding+f.layers[j].Size > layerBudget {
			break
		}

		outstanding += f.layers[j].Size

		ch := make(chan layerBlob, 1)
		f.inflight[j] = ch

		go func(d descriptor) {
			blob, err := fetchLayer(f.ctx, f.client, f.tok, f.base, d)
			ch <- layerBlob{blob: blob, err: err}
		}(f.layers[j])
	}

	got := <-f.inflight[i]
	delete(f.inflight, i)

	return got
}

// fetchLayer gets one layer's blob and checks it is the one that was asked for.
func fetchLayer(ctx context.Context, client *http.Client, tok, base string, d descriptor) ([]byte, error) {
	limit := d.Size
	if limit <= 0 {
		limit = 1 << 30
	}

	endGet := timing.Phase("layer:get", d.Digest)
	blob, err := get(ctx, client, tok, base+"/blobs/"+d.Digest, limit)

	endGet()

	if err != nil {
		return nil, err
	}

	err = verify(blob, d.Digest)
	if err != nil {
		return nil, err
	}

	return blob, nil
}

// unpackLayer applies one fetched layer to the directory being built.
func unpackLayer(blob []byte, d descriptor, dir string) error {
	_, err := unpackOneLayer(blob, d, dir, false)

	return err
}

// unpackLayerApart writes one layer into a directory of its own, reporting
// whether it carries deletion markers. See UnpackApart for why the two differ.
func unpackLayerApart(blob []byte, d descriptor, dir string) (Unpacked, error) {
	return unpackOneLayer(blob, d, dir, true)
}

func unpackOneLayer(blob []byte, d descriptor, dir string, apart bool) (Unpacked, error) {
	r, err := decompress(blob, d.MediaType)
	if err != nil {
		return Unpacked{}, err
	}

	defer r.Close()

	defer timing.Phase("layer:unpack", d.Digest)()

	if apart {
		return UnpackApart(r, dir)
	}

	return Unpacked{}, Unpack(r, dir)
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
func token(ctx context.Context, client *http.Client, url, dir, key string) (string, error) {
	defer timing.Phase("registry:token", key)()

	// Where this repository's token came from last time. A stale answer costs a
	// probe rather than a build: the exchange below runs and replaces it.
	if at := rememberedChallenge(dir, key); at != "" {
		// **The registry is dialled while the token is fetched.** They are
		// different hosts, so the two TLS handshakes have nothing to say to each
		// other and no reason to happen one after the other. The probe this
		// remembered answer replaces had been doing this dial as a side effect,
		// which is why deleting it returned less than its own phase claimed
		// (E535).
		done := warm(ctx, client, url)

		tok, err := fetchToken(ctx, client, at)

		done()

		if err == nil && tok != "" {
			return tok, nil
		}
	}

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

	at := fmt.Sprintf("%s?service=%s&scope=%s", realm, service, scope)

	tok, err := fetchToken(ctx, client, at)
	if err != nil {
		return "", err
	}

	// Remembered only once it has worked, so a realm that answers with nothing
	// useful is not the answer the next build starts from.
	rememberChallenge(dir, key, at)

	return tok, nil
}

// warm opens a connection to the registry a manifest is about to be fetched
// from, and returns a function that waits for it.
//
// `/v2/` is the registry's own endpoint and the cheapest thing it will answer:
// this wants the connection, not the response, and reads the body only so the
// transport can pool it. Any failure is ignored - the request that follows will
// dial for itself, which is what it did before this existed.
func warm(ctx context.Context, client *http.Client, manifestURL string) func() {
	at := strings.Index(manifestURL, "/v2/")
	if at < 0 {
		return func() {}
	}

	ping := manifestURL[:at+len("/v2/")]
	done := make(chan struct{})

	go func() {
		defer close(done)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ping, nil)
		if err != nil {
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			return
		}

		defer resp.Body.Close()

		// Drained, not abandoned: a body left unread is a connection the
		// transport will not reuse, which loses the only thing this is for.
		_, _ = io.Copy(io.Discard, resp.Body)
	}()

	// Waited for, so the connection is in the pool before the manifest asks for
	// one - and so this goroutine never outlives the call that started it.
	return func() { <-done }
}

// fetchToken asks a realm for a token.
func fetchToken(ctx context.Context, client *http.Client, at string) (string, error) {
	body, err := get(ctx, client, "", at, maxManifest)
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

// PulledLayer is one layer of an image, unpacked into a directory of its own.
type PulledLayer struct {
	// Digest is the layer's digest as the manifest gave it.
	Digest string
	// Dir is the directory it was unpacked into, relative to the root handed
	// to PullApart.
	Dir string
	// Marked reports whether the layer carries deletion markers, which the
	// unpacker knows for nothing because it read every entry. Without it the
	// materialiser walks the whole layer to ask the same question - 1.44s of a
	// cold `golang:1.26-alpine` pull, once per layer.
	Marked bool
	// Digests is each regular file's content digest, hashed as it was written.
	// Without it the store reads the whole layer back to compute the same
	// numbers - 0.958s of that same pull. See Unpacked.
	Digests map[string]Digest
	// Owners is the archive's account of who owns each path, which the disk
	// cannot give back: an unprivileged unpack could not grant it (E656).
	Owners map[string]Owner
	// MediaType is how the layer's blob is compressed. Carried because a
	// retained blob is unreadable without it, and guessing gzip fails inside
	// the unpacker with a complaint about a corrupt archive - the wrong
	// component entirely.
	MediaType string
}

// PullApart fetches an image and unpacks each layer into a directory of its
// own, returning them in the order they must be stacked.
//
// **The merge is what makes unpacking serial.** `Pull` puts every layer into
// one directory oldest first, so a later layer may overwrite an earlier one and
// the order cannot be given up. Nothing about fetching or unpacking a layer
// depends on another layer - only the merge does - so keeping them apart lets
// all of it happen at once, and the assembling becomes an overlay mount, which
// is what overlayfs is for (E646).
//
// The layers are unpacked concurrently when no single one dominates, and
// serially when one does: a layer that is most of the image has nothing to
// overlap with, and four goroutines competing for one disk cost 23% on
// `golang:1.26-alpine` where the ceiling was 4% (E646). The manifest carries
// the sizes, so the choice is made before a byte is fetched.
func PullApart(ctx context.Context, ref, root string, opt Options) ([]PulledLayer, ocispec.ImageConfig, error) {
	return pullApart(ctx, ref, root, opt)
}

// unpackAtOnceBelow is how much of an image one layer may be before unpacking
// them together stops being worth it.
//
// **Measured, and the floor is worse than the ceiling.** Where no layer
// dominates, unpacking at once delivers what the arithmetic promises - 37%
// predicted and 38% measured on `python:3.13-slim`. Where one does, there is
// nothing to overlap with and the goroutines merely contend: `golang:1.26-alpine`
// keeps 96% of its unpack in one layer and went 23% *slower* (E646).
//
// The manifest carries every layer's size, so the choice costs nothing and is
// made before a byte is fetched.
const unpackAtOnceBelow = 0.80

// worthUnpackingAtOnce reports whether an image's layers are even enough that
// unpacking them together will pay.
func worthUnpackingAtOnce(layers []descriptor) bool {
	if len(layers) < 2 {
		return false
	}

	var total, largest int64

	for _, d := range layers {
		total += d.Size
		if d.Size > largest {
			largest = d.Size
		}
	}

	if total <= 0 {
		return false
	}

	return float64(largest)/float64(total) < unpackAtOnceBelow
}

// layerDir is where one layer of an image lands, under the root PullApart was
// given.
//
// Numbered as well as digested, so a listing is in stacking order and a person
// reading it can see which layer is which without consulting the manifest.
func layerDir(i int, digest string) string {
	_, hexsum, _ := strings.Cut(digest, ":")
	if len(hexsum) > 12 {
		hexsum = hexsum[:12]
	}

	return fmt.Sprintf("%02d-%s", i, hexsum)
}

// pullApart is PullApart's body.
func pullApart(ctx context.Context, ref, root string, opt Options) ([]PulledLayer, ocispec.ImageConfig, error) {
	p, err := prepare(ctx, ref, opt)
	if err != nil {
		return nil, ocispec.ImageConfig{}, err
	}

	err = os.MkdirAll(root, 0o750)
	if err != nil {
		return nil, ocispec.ImageConfig{}, fmt.Errorf("create the unpack directory: %w", err)
	}

	layers := p.m.Layers
	out := make([]PulledLayer, len(layers))
	failed := make([]error, len(layers))
	atOnce := worthUnpackingAtOnce(layers)

	endUnpack := timing.Phase("layers:unpack", fmt.Sprintf("%d apart, at once=%v", len(layers), atOnce))

	// **Streamed, every layer at once, with no byte budget.** The budget exists
	// to bound how much of an image is held in memory while it waits for the
	// unpacker; a streamed layer holds a buffer instead of a blob, so there is
	// nothing to bound and nothing to wait for. Each layer's fetch overlaps its
	// own unpack and every other layer's - which is the whole point.
	if opt.Stream {
		var streaming sync.WaitGroup

		for i, d := range layers {
			sub := layerDir(i, d.Digest)

			mkErr := os.MkdirAll(filepath.Join(root, sub), 0o750)
			if mkErr != nil {
				streaming.Wait()

				return nil, ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, mkErr)
			}

			out[i] = PulledLayer{Digest: d.Digest, Dir: sub, MediaType: d.MediaType}

			streaming.Add(1)

			go func(i int, d descriptor, into string) {
				defer streaming.Done()

				var got Unpacked

				got, failed[i] = streamLayerApart(ctx, p.client, p.tok, p.base, d, into, opt.Retain)
				out[i].Marked, out[i].Digests, out[i].Owners = got.Marked, got.Digests, got.Owners
			}(i, d, filepath.Join(root, sub))
		}

		streaming.Wait()
		endUnpack()

		return finishApart(ctx, p, opt, ref, out, failed)
	}

	fetching := newLayerFetch(ctx, p.client, p.tok, p.base, layers)

	var wg sync.WaitGroup

	for i, d := range layers {
		got := fetching.await(i)
		if got.err != nil {
			wg.Wait()

			return nil, ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, got.err)
		}

		sub := layerDir(i, d.Digest)
		into := filepath.Join(root, sub)

		err = os.MkdirAll(into, 0o750)
		if err != nil {
			wg.Wait()

			return nil, ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, err)
		}

		out[i] = PulledLayer{Digest: d.Digest, Dir: sub, MediaType: d.MediaType}

		keepBlob(opt.Retain, d.Digest, got.blob)

		if !atOnce {
			var un Unpacked

			un, failed[i] = unpackLayerApart(got.blob, d, into)
			out[i].Marked, out[i].Digests, out[i].Owners = un.Marked, un.Digests, un.Owners

			continue
		}

		wg.Add(1)

		// Each goroutine writes only its own slot, so no lock: the slices are
		// sized before the fan-out and indices are never reused.
		go func(i int, d descriptor, into string, blob []byte) {
			defer wg.Done()

			var un Unpacked

			un, failed[i] = unpackLayerApart(blob, d, into)
			out[i].Marked, out[i].Digests, out[i].Owners = un.Marked, un.Digests, un.Owners
		}(i, d, into, got.blob)
	}

	wg.Wait()
	endUnpack()

	return finishApart(ctx, p, opt, ref, out, failed)
}

// finishApart reports the first layer that failed, or fetches the configuration.
//
// Shared by the buffered and streamed paths so that a layer failure reads the
// same either way: which layer, of which image, and why.
func finishApart(ctx context.Context, p prepared, opt Options, ref string,
	out []PulledLayer, failed []error,
) ([]PulledLayer, ocispec.ImageConfig, error) {
	for i, e := range failed {
		if e != nil {
			return nil, ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, e)
		}
	}

	cfg, err := pullConfig(ctx, p.client, p.tok, p.base, p.m.Config, opt.Platform)
	if err != nil {
		return nil, ocispec.ImageConfig{}, err
	}

	return out, cfg, nil
}

// keepBlob files a layer's compressed bytes where the caller asked.
//
// Best effort throughout: a retention that fails leaves a pull that worked,
// because the layer is unpacked either way and the blob is an optimisation for
// the *next* build. Failing here would trade a working pull for a tidier cache.
func keepBlob(retain func(string) (io.WriteCloser, error), digest string, blob []byte) {
	if retain == nil {
		return
	}

	w, err := retain(digest)
	if err != nil {
		return
	}

	_, err = w.Write(blob)
	if err != nil {
		_ = w.Close()

		return
	}

	_ = w.Close()
}

// FetchedLayer is one layer's compressed bytes, on disk and unread.
type FetchedLayer struct {
	// Digest is the layer's digest as the manifest gave it, which is also what
	// the bytes were checked against.
	Digest string
	// MediaType is how they are compressed. Carried because a blob whose
	// compression nobody recorded cannot be read, and guessing gzip fails inside
	// the unpacker with a complaint about a corrupt archive - the wrong
	// component entirely.
	MediaType string
	// At is where they were written, relative to the root FetchApart was given.
	At string
}

// FetchApart fetches an image's layers as blobs and unpacks none of them.
//
// **The half of a pull that belongs on the host.** The network, the credentials
// and the manifest are here; the filesystem that can hold what an archive
// declares is not - an unprivileged unpack cannot grant ownership, create a
// device node, or set an attribute in the `security.` namespace, and the layer
// store is moving onto the block device the guest owns for reasons that have
// nothing to do with privilege (E511, E676, E677).
//
// So this stops at the bytes. What comes back is enough to ask the guest to
// unpack them: where each layer is, how it is compressed, and in what order
// they stack.
//
// Blobs on a shared mount are a good trade even when trees are not: one large
// sequential read against fifteen thousand small writes.
func FetchApart(
	ctx context.Context, ref, root string, opt Options,
) ([]FetchedLayer, ocispec.ImageConfig, error) {
	p, err := prepare(ctx, ref, opt)
	if err != nil {
		return nil, ocispec.ImageConfig{}, err
	}

	err = os.MkdirAll(root, 0o750)
	if err != nil {
		return nil, ocispec.ImageConfig{}, fmt.Errorf("create the blob directory: %w", err)
	}

	layers := p.m.Layers
	out := make([]FetchedLayer, len(layers))

	fetching := newLayerFetch(ctx, p.client, p.tok, p.base, layers)

	for i, d := range layers {
		got := fetching.await(i)
		if got.err != nil {
			return nil, ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, got.err)
		}

		// Named for the layer rather than its position: two images sharing a
		// layer share the file, and a second fetch of one finds it already
		// there under a name that cannot be mistaken for another's.
		at := blobFile(d.Digest)

		err = os.WriteFile(filepath.Join(root, at), got.blob, 0o600)
		if err != nil {
			return nil, ocispec.ImageConfig{}, fmt.Errorf("layer %d of %s: %w", i, ref, err)
		}

		out[i] = FetchedLayer{Digest: d.Digest, MediaType: d.MediaType, At: at}
	}

	cfg, err := pullConfig(ctx, p.client, p.tok, p.base, p.m.Config, opt.Platform)
	if err != nil {
		return nil, ocispec.ImageConfig{}, err
	}

	return out, cfg, nil
}

// blobFile names a layer's compressed bytes on disk.
//
// The digest with its algorithm prefix turned into a directory separator would
// be two levels; flattened instead, because these sit beside each other and a
// colon is a character some filesystems would rather not see.
func blobFile(digest string) string {
	algo, hexsum, ok := strings.Cut(digest, ":")
	if !ok {
		return "blob-" + digest
	}

	return algo + "-" + hexsum
}
