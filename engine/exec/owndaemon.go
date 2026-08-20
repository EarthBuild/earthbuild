package exec

import (
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// daemonRoot is where a step's own daemon keeps everything, inside the step.
//
// Under `/var/lib` because that is where a daemon's storage belongs and an
// author who goes looking will look there - and named for this engine rather
// than `docker`, because a step's image may have a real `/var/lib/docker` in it
// and a mount landing on top of one would hide what the image shipped.
const daemonRoot = "/var/lib/earthbuild-docker"

// daemonSocket is where a step's own daemon listens, inside the step.
//
// The conventional path, because the step's client looks there by default and an
// image that hard-codes it is common. It is the step's own `/var/run`, not the
// machine's - the guest resolves it against the step's root (E370).
const daemonSocket = "/var/run/docker.sock"

// ownDaemonMounts is what a step needs to run a daemon of its own, and where
// that daemon should keep its storage.
//
// **Fewer mounts than the experiment suggested.** E364 started a daemon by hand
// with a tmpfs over `/run`, because that attempt ran in the *host's* mount
// namespace where `/run` belongs to the machine. A step's root is an overlay and
// is writable already, so the tmpfs was an artefact of the experiment rather
// than a requirement of the daemon (E365).
//
// What is left is the storage, and it is the whole of what `--cache-id` means:
//
//   - **named**: the directory that name derives to (E360) is mounted in, and
//     the daemon's root is inside it - so two blocks naming one cache see each
//     other's images and two naming different ones do not, which is the half the
//     host's daemon cannot give (E362);
//   - **unnamed**: nothing is mounted, and the daemon writes into the step's own
//     filesystem, which goes away with the step. That is the isolation E354
//     promised - not a flag, the absence of one.
func ownDaemonMounts(cache string) ([]guest.Mount, string) {
	// Mounted either way, because a mount is what keeps the daemon's storage out
	// of the image: a step's overlay is what the capture turns into a layer, so
	// leaving the storage there ships it - every image the daemon held, and a
	// `docker.pid` that makes the next step refuse to start (E398).
	//
	// What the name changes is only whether the directory outlives the step.
	if cache == "" {
		return []guest.Mount{{Target: daemonRoot, Ephemeral: true}}, daemonRoot
	}

	return []guest.Mount{{
		ID:     filepath.Join("docker-cache", cache),
		Target: daemonRoot,
	}}, daemonRoot
}
