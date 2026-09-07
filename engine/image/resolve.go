package image

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/timing"
)

// Resolve answers what a reference names right now: the same repository at a
// digest.
//
// This is Θ (green paper §3.4d). It is the only observation a build makes that
// no key can be closed over - a registry's answer at an instant is not a
// property of this machine - so it is made once, early, and recorded. Everything
// downstream keys on what comes back, which is what makes a moved tag a
// different build rather than a stale hit on the same one (I3).
//
// **A reference that already names a digest is returned unchanged**, without
// asking anybody. It is already an answer, and confirming it would make a fully
// pinned build depend on the registry being reachable - which is most of what
// pinning is for.
//
// Only the manifest is fetched. No blob is read and nothing is written to any
// store, so this is one round trip per distinct reference and safe to do for
// every reference in a graph at once.
func Resolve(ctx context.Context, ref string, opt Options) (string, error) {
	r, err := ParseRef(ref)
	if err != nil {
		return "", err
	}

	if r.Digest != "" {
		return ref, nil
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

	// **The origin, and deliberately not a mirror.** A pull may take its bytes
	// from anywhere because every digest is verified against the manifest; a
	// resolution *is* the answer to "what does this tag mean today", and a
	// mirror's answer is its own cache. Pinning to a stale digest would be
	// worse than not pinning at all, so this asks the registry itself.
	//
	// Not timed here: `token` opens a `registry:token` phase of its own, so a
	// phase around this call reports the same round trip twice - two lines
	// agreeing to the millisecond, which anybody reading the log as a list of
	// costs will add together (E733). The inner one also names the repository
	// where this named only the registry.
	tok, err := token(ctx, client, base+"/manifests/"+r.Tag, opt.Challenges, challengeKey(r))

	if err != nil {
		return "", fmt.Errorf("authenticate to %s: %w", r.Registry, err)
	}

	endManifest := timing.Phase("pin:manifest", ref)
	body, err := get(ctx, client, tok, base+"/manifests/"+r.Tag, maxManifest)
	endManifest()
	if err != nil {
		return "", fmt.Errorf("fetch the manifest for %s: %w", ref, err)
	}

	var m manifest

	err = json.Unmarshal(body, &m)
	if err != nil {
		return "", fmt.Errorf("parse the manifest for %s: %w", ref, err)
	}

	// The digest of the *image*, not of the index. A multi-platform tag names a
	// list, and pinning the list would leave the choice of image open - which is
	// the thing being closed.
	pinned := DigestOf(body)

	if len(m.Manifests) > 0 && !opt.Index {
		want := opt.Platform
		if want == "" {
			want = runtime.GOOS + "/" + runtime.GOARCH
		}

		pinned, err = selectPlatform(m, want)
		if err != nil {
			return "", fmt.Errorf("%s: %w", ref, err)
		}
	}

	return at(ref, pinned), nil
}

// at is the reference with its tag replaced by a digest.
//
// Written by hand rather than reassembled from the parsed parts: a reference
// carries a registry that may have been defaulted and a repository that may have
// been expanded, and rebuilding it from those would return something that names
// the same image and is not the string the caller wrote.
func at(ref, digest string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		ref = ref[:i]
	}

	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}

	return ref + "@" + digest
}
