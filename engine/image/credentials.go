package image

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/types"
)

// credential is what this machine knows about one registry.
//
// The same credentials the buildkit path uses, resolved through the same
// library: `cmd/earth` hands BuildKit a session attachable built from
// `config.LoadDefaultConfigFile`, and BuildKit calls back to it when a registry
// refuses. This engine talks to registries in its own process, so there is
// nobody to call back to - it asks the same question directly. Two engines
// reading one credential store is the point; two engines with two ideas of
// where credentials live is the thing worth avoiding.
type credential struct {
	User   string
	Secret string
	// IdentityOnly marks a registry that gave docker an OAuth2 refresh token
	// rather than a password. It cannot be sent as one - see credentialFrom.
	IdentityOnly bool
}

func (c credential) empty() bool { return c.User == "" && c.Secret == "" }

// holdKey is where this credential's token lives in the per-process cache.
//
// **Keyed by who asked, not only where.** The endpoint URL already carries the
// scope, so two repositories cannot share an entry - but two *users* against
// one repository could, and the second would be handed the first one's access.
// An anonymous fetch and an authenticated one are likewise different questions
// with different answers.
func (c credential) holdKey(at string) string {
	if c.empty() {
		return at
	}

	return at + "\x00" + c.User
}

// credentialFrom reads docker's answer, which is not always a password.
//
// A registry that issued an *identity token* gave docker an OAuth2 refresh
// token, redeemed by a POST to the realm with `grant_type=refresh_token`. This
// engine does the GET exchange only, so sending it as a password would present
// a credential in a form the registry does not accept and report whatever it
// made of that. Better to carry the fact and say so where it can be acted on.
func credentialFrom(a types.AuthConfig) credential {
	if a.IdentityToken != "" && a.Password == "" {
		return credential{User: a.Username, IdentityOnly: true}
	}

	return credential{User: a.Username, Secret: a.Password}
}

// authHost is the name docker files a registry's credentials under.
//
// **Docker Hub is filed somewhere other than where it is dialled.** This engine
// requests from `registry-1.docker.io` (see registryHost), and docker's own
// mapping recognises `docker.io` and `index.docker.io` only - so asking under
// the host actually dialled misses a `docker login` that plainly happened, and
// misses it silently, which is the worst way to miss it.
func authHost(host string) string {
	if host == dockerHubHost {
		return dockerHubDomain
	}

	return host
}

// dockerConfig is read once. Resolving a credential can exec a helper - the
// keychain on a Mac - and a build asking per reference would pay for that per
// reference.
//
// A variable rather than a call so a test can put a config of its own in front
// of it: the interesting behaviour is what this engine does with what docker
// stored, and a test that can only read the developer's own login tests the
// developer's machine.
var dockerConfig = sync.OnceValue(func() *configfile.ConfigFile {
	// Warnings go nowhere: this is a best-effort lookup on a path that works
	// without any credentials at all, and a note about a malformed config would
	// land in the middle of an unrelated build.
	return config.LoadDefaultConfigFile(io.Discard)
})

// credentials memoises per host, for the reason dockerConfig is read once.
//
// Holds a slot rather than a value, so the *resolution* happens once and not
// merely the storing of it - see credentialFor.
var credentials sync.Map

// heldCredential is one host's answer, resolved at most once however many
// goroutines want it.
type heldCredential struct {
	once sync.Once
	val  credential
}

// resolveCredential is the expensive half, named so a test can count it.
var resolveCredential = func(host string) credential {
	return lookupIn(dockerConfig(), host)
}

// credentialFor is what this machine can prove about one registry, or nothing.
//
// **Never an error.** A machine with no docker config, an unreadable one, or a
// helper that fails is a machine that pulls anonymously - which is what this
// engine did before credentials existed and is right for every public image. A
// registry that genuinely needs one refuses, and that refusal is the diagnostic.
func credentialFor(host string) credential {
	// **Resolved once per host, not merely stored once.** A build resolves its
	// references concurrently - one goroutine per image - and a memo that reads,
	// computes and then stores leaves a window every one of them fits through:
	// six images from one registry would exec the credential helper six times to
	// learn the same thing. LoadOrStore settles which slot, and the slot's Once
	// settles who fills it; the rest wait for an answer they were going to wait
	// for anyway.
	slot, _ := credentials.LoadOrStore(authHost(host), &heldCredential{})

	held, ok := slot.(*heldCredential)
	if !ok {
		return resolveCredential(host)
	}

	held.once.Do(func() { held.val = resolveCredential(host) })

	return held.val
}

// lookupIn asks one config about one registry, under the name that config files
// it under.
//
// Separated from credentialFor so the mapping and the resolution can be tested
// together against docker's own lookup rather than a stand-in: authHost can be
// right and the question still be put wrongly, or the reverse, and neither shows
// up in a test of either half alone.
func lookupIn(cfg *configfile.ConfigFile, host string) credential {
	a, err := cfg.GetAuthConfig(authHost(host))
	if err != nil {
		// A helper that fails is a machine that pulls anonymously. See
		// credentialFor: the registry's own refusal is the diagnostic.
		return credential{}
	}

	return credentialFrom(a)
}

// credentialForURL is the credential for the registry a request is going to.
//
// A URL rather than a host because that is what the caller has, and parsing it
// here keeps one reading of it: a host taken two ways is how `docker.io` and
// `registry-1.docker.io` came to mean different things in the first place.
func credentialForURL(raw string) credential {
	u, err := url.Parse(raw)
	if err != nil {
		return credential{}
	}

	// **Host and not Hostname: the port is part of the name.** Docker files a
	// registry on a non-default port under `host:port` - `localhost:5000` is the
	// ordinary self-hosted case - so dropping it looks up a name nothing was
	// ever stored under, and the login silently does not apply. `Host` keeps an
	// explicit port and omits an implicit one, which is the same rule docker
	// wrote the key with.
	return credentialFor(u.Host)
}

// fetchTokenAs performs the token exchange, presenting a credential when there
// is one.
//
// **The credential goes in a header and never in the URL.** The token endpoint
// is printed verbatim by the "was not pinned" note, so a credential folded into
// a query parameter would be published by a routine diagnostic rather than by
// anything anyone would call a leak.
func fetchTokenAs(ctx context.Context, client *http.Client, at string, cred credential) (string, error) {
	key := cred.holdKey(at)

	if tok, ok := tokens.get(key); ok {
		return tok, nil
	}

	// **One exchange, however many callers want it.** Without this, "fetch the
	// token early so the pull finds it cached" (E907) only helps when something
	// delays the pull. On macOS a 1.4s VM boot does; on Linux there is no VM,
	// the pull starts beside the warm, both miss the cache, and the build makes
	// two full exchanges where it used to make one - measured on the x86 box,
	// and an extra request against a rate limit for no gain (E915).
	//
	// The cost is that concurrent callers share the first one's context and its
	// failure. Both are acceptable here: the contexts belong to the same build,
	// and callers that would have failed separately now fail together.
	v, err, _ := tokenFlight.Do(key, func() (any, error) {
		// Re-checked inside the flight: a caller may have filled the cache
		// between the check above and being admitted here.
		if tok, ok := tokens.get(key); ok {
			return tok, nil
		}

		return fetchTokenNow(ctx, client, at, cred)
	})
	if err != nil {
		return "", err
	}

	tok, _ := v.(string)

	return tok, nil
}

// tokenFlight collapses concurrent requests for the same token endpoint.
var tokenFlight singleflight.Group

// fetchTokenNow performs the exchange, with no cache and no sharing.
func fetchTokenNow(ctx context.Context, client *http.Client, at string, cred credential) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, at, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	if !cred.empty() {
		req.SetBasicAuth(cred.User, cred.Secret)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request %s: %w", at, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", tokenRefusal(at, resp.StatusCode, cred)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifest))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", at, err)
	}

	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}

	err = json.Unmarshal(body, &t)
	if err != nil {
		return "", fmt.Errorf("decode token from %s: %w", at, err)
	}

	tok := t.Token
	if tok == "" {
		tok = t.AccessToken
	}

	tokens.put(cred.holdKey(at), tok)

	return tok, nil
}

// tokenRefusal says what to do about it, which the status alone does not.
//
// A 401 or 403 here reads as "log in", and until this engine read docker's
// credentials that advice was wrong in a way no amount of retrying revealed.
// It is right now, so the message says it - and says the other two things it
// can know: which registry, and whether a credential was actually presented.
func tokenRefusal(at string, status int, cred credential) error {
	switch {
	case cred.IdentityOnly:
		return fmt.Errorf("%s returned %d, and the stored credential is an identity token"+
			"\n  docker holds an OAuth2 refresh token for this registry, which this engine"+
			" cannot redeem - it performs the GET exchange only"+
			"\n  `docker login` again with a password or an access token to store one",
			at, status)

	case cred.empty():
		return fmt.Errorf("%s returned %d, and no credential was presented"+
			"\n  nothing is stored for this registry: `docker login <registry>` and build again"+
			"\n  a private image needs one; a public image does not",
			at, status)

	default:
		return fmt.Errorf("%s returned %d for %s"+
			"\n  a credential was presented and refused, so it is stored but not accepted here"+
			"\n  `docker login <registry>` again, or check the account can pull this repository",
			at, status, cred.User)
	}
}
