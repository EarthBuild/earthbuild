package exec

import "github.com/EarthBuild/earthbuild/engine/guest"

// hostDockerSocket is the conventional path a daemon listens on, and the only
// place an inherited one is looked for.
//
// Not `DOCKER_HOST`: an environment variable set for the build is the *build's*
// choice of daemon, and this asks what the step is standing inside. A
// container's socket is at this path because the outer step put it there.
const hostDockerSocket = "/var/run/docker.sock"

// withSocket completes a plan: the socket an inheriting step reaches its daemon
// through, and the client either kind of step needs.
//
// **The decision and its consequence are separate code, which is the hazard.**
// `dockerPlanFor` decides to share; without the mount the step is told it is
// sharing, finds no socket, and reports a daemon that is not running for one
// that is - indistinguishable from the feature never having been asked for.
//
// A step with a daemon of its own is given no socket here. Its own binds one
// inside the step's filesystem at the same path (E370), and two mounts at one
// path would be resolved by whichever the guest applied last: isolation that
// depends on mount ordering is not isolation.
//
// The client is offered either way and its absence is not fatal - an image often
// carries its own, and the daemon is the part no image can supply (E145).
func withSocket(p dockerPlan, client []guest.Mount) dockerPlan {
	if p.Inherit {
		p.Mounts = append(p.Mounts,
			guest.Mount{Sandbox: hostDockerSocket, Target: hostDockerSocket})
	}

	p.Mounts = append(p.Mounts, client...)

	return p
}

// dockerPlan is what a WITH DOCKER step is given.
//
// Own and Inherit are exclusive by construction rather than by convention: a
// step holding both an inherited socket and a daemon of its own would use
// whichever its client happened to look at first, and which one that is depends
// on the image.
type dockerPlan struct {
	// Own says the step starts a daemon of its own, and Mounts is what it needs
	// to do so - a named cache's directory, or nothing at all, which is what
	// makes an unnamed one's storage die with the step (E365).
	Own    bool
	Mounts []guest.Mount
	// Inherit says the step reaches a daemon that is already running around it.
	Inherit bool
	// Note is a warning about the docker *client* - absent, unusable - or empty.
	//
	// Only that. It reaches `warnNoDockerClient` through `DockerNote`, which
	// exists to explain a step that will say `docker: not found` (E146). The
	// first version of this field carried a routine explanation of which daemon
	// the block got, which made a build warn about a client that was present and
	// fine (E392).
	//
	// Which daemon a block got is worth telling an author and has nowhere to go
	// yet: the channels that exist are a warning and a failure, and this is
	// neither. Said here rather than smuggled into one of them.
	Note string
}

// dockerPlanFor decides which daemon a WITH DOCKER step gets.
//
// **The mode is decided by the block and its surroundings together.** In the
// decided polarity (E381) a bare block shares, but sharing needs something to
// share with: inside a WITH DOCKER step there is a daemon around this build and
// the block uses it, and on a machine with nothing around it the block starts
// its own. The same Earthfile, two surroundings, and no flag for either -
// nesting by not nesting (E377).
//
// `--isolate` takes its own whatever is around it, and `--cache-id` does too,
// with its storage in the named directory. Those two are refused together by the
// interpreter, so the cache implies a daemon of this step's own.
// **No error, and none is possible.** Every path ends in a daemon: where an
// outer one may not be used - a socket on a machine this build is not
// containerised on is the machine's own, root on it (E145) - the block gets one
// of its own instead, which is strictly better than E354's refusal and needs
// nobody's permission. An error return here could never fire, and one that
// cannot fire is a claim the code does not support (E368).
func dockerPlanFor(isolate bool, cache, scope string, inside, socket, allowed bool) dockerPlan {
	if share, _ := outerDaemonUsable(inside, socket, allowed); share && !isolate && cache == "" {
		return dockerPlan{
			Inherit: true,
		}
	}

	mounts, _ := ownDaemonMounts(cache, scope)

	return dockerPlan{Own: true, Mounts: mounts}
}
