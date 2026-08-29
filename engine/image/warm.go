package image

import (
	"context"
	"fmt"
	"net/http"
)

// Warm performs a registry's authentication handshake before the pull needs it.
//
// **The exchange has no reason to wait behind the sandbox.** A cold build boots
// a VM for 1.48s and then fetches the image, and the first 0.46s of that fetch
// is `registry:token` - a TLS handshake and a round trip to a token service,
// entirely on the host. It is paid even when the reference is pinned by digest,
// because pinning removes the *resolution* and not the *pull*. Run beside the
// boot instead of behind it, a cold build pays for the longer of the two
// (E907). Same argument as Prewarm, one layer out.
//
// Returns nothing, and swallows every error, for Prewarm's reason: this is an
// optimisation, so a warm that cannot work must leave a build that is slower
// rather than one that stops. Whatever is wrong is reported by the pull that
// follows, which has the context to say it properly.
//
// Safe to call for a reference that is never pulled: the cost is one exchange
// against a token cache that a later pull would have filled anyway.
func Warm(ctx context.Context, ref string, opt Options) {
	r, err := ParseRef(ref)
	if err != nil {
		return
	}

	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}

	scheme := schemeHTTPS
	if opt.Plain {
		scheme = schemePlain
	}

	// The manifest URL is what the challenge is issued against, so it must be
	// the URL the pull will use. A pinned reference names its digest here and
	// asking for the tag instead would draw a challenge for a different scope
	// on some registries - a token that then fails to authenticate the thing it
	// was fetched for, which is worse than not warming at all.
	what := r.Tag
	if r.Digest != "" {
		what = r.Digest
	}

	if what == "" {
		return
	}

	base := fmt.Sprintf("%s://%s/v2/%s", scheme, registryHost(r.Registry), r.Repository)

	// The token lands in the process-wide cache the pull reads
	// (`tokencache.go`), which is what makes this a move rather than an extra
	// exchange - the property TestWarmingDoesNotAddATokenExchange pins.
	_, _ = token(ctx, client, base+"/manifests/"+what, opt.Challenges, challengeKey(r))
}
