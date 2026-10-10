package image

import (
	"os"
	"strings"
)

// EnvMirrors names hosts to ask before Docker Hub, most preferred first.
//
// **Off unless asked.** Docker Hub allows an anonymous puller 100 manifest
// requests an hour and a build behind one address exhausts that, after which
// every `FROM` fails outright - the slowest a build can be. A mirror removes
// that wall, which is why CI already fronts buildkitd with `mirror.gcr.io`.
//
// It is opt-in because a mirror answers "what does this tag mean" from its own
// cache: bytes are safe wherever they come from, since every digest is checked,
// but a moving tag may resolve to an older image than the origin would give.
// Turning that on for everybody by default would change which image a build gets
// without anybody having said so.
const EnvMirrors = "EARTH_REGISTRY_MIRRORS"

// MirrorsFromEnv reads EnvMirrors into the form Options.Mirrors takes.
//
// Docker Hub only: it is the registry that rate-limits, and the one every mirror
// in existence fronts. A private registry wanting the same thing can be given it
// when somebody has one.
func MirrorsFromEnv() map[string][]string {
	var hosts []string

	for h := range strings.SplitSeq(os.Getenv(EnvMirrors), ",") {
		// A person writes a URL and a host is not one; taken literally this
		// builds `https://https://mirror.gcr.io/v2/...`, which resolves to
		// nothing and reads like a bug in the mirror.
		h = strings.TrimSpace(h)
		h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
		h = strings.Trim(h, "/")

		if h != "" {
			hosts = append(hosts, h)
		}
	}

	if len(hosts) == 0 {
		return nil
	}

	return map[string][]string{"docker.io": hosts}
}
