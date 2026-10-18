package exec

import (
	"os"
	"strings"
)

// EnvTrustDomain names the set of writers this build's cache entries belong to.
//
// **The engine cannot work this out and must not guess.** Whether a build is
// trusted is a fact about the repository's policy - who may open a pull request,
// which branches are protected - and it lives in the CI configuration, not in
// anything an Earthfile or a sandbox can see. So it is told, and an untold
// domain means the single implicit one every build has always shared (I10: the
// engine refuses to approximate rather than inventing an answer).
//
// Set it to something stable per trust level and *not* per run: a value that
// changed every build would isolate every build from every other, which is a
// cache nobody ever hits rather than a security property.
//
//	EARTH_TRUST_DOMAIN=trusted        # protected branches
//	EARTH_TRUST_DOMAIN=fork           # pull requests from forks
const EnvTrustDomain = "EARTH_TRUST_DOMAIN"

// trustDomain is what this build's caches are isolated by, or empty.
//
// Whitespace is trimmed because a value arriving from a CI template commonly has
// some, and `"fork "` isolating differently from `"fork"` would be an isolation
// nobody asked for and nobody could see.
func trustDomain() string { return strings.TrimSpace(os.Getenv(EnvTrustDomain)) }
