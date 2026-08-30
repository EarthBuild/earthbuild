package guest

import "path/filepath"

// stepShimFlag marks this process as the shim that prepares a step.
//
// Distinct from daemonShimFlag because the two prepare different things for
// different children, and a process that answered to both would be one flag away
// from mounting a daemon's `/run` over a step's filesystem.
const stepShimFlag = "--earthbuild-step-shim"

// EnvStepTraceFD names the descriptor the shim hands its seccomp listener back
// on, and is empty for a step nobody is watching.
//
// **The install happens here rather than in the guest, and that is the point.**
// A filter installed before the clone is inherited by this process, so this
// process's own `execve` - the guest exec'ing the shim, the child's very first
// syscall - traps to a supervisor that the `CLONE_VFORK` is preventing from
// running, and neither side moves again (E723, E729).
//
// Installing here instead means the guest clones with no filter anywhere, the
// vfork is released as soon as this binary execs, and the only `execve` that
// traps is the step's own - by which time the guest is an ordinary process that
// can answer it.
//
// An environment variable rather than another argv slot: `stepShimAsked` reads
// the step's command from a fixed index, and a field added before it would
// silently reinterpret the argv of every step.
const EnvStepTraceFD = "EARTH_STEP_TRACE_FD"

// EnvStepTracePin is the CPU the step should be pinned to, and is empty unless
// EARTH_TRACE_PIN asked for pinning.
//
// **The pin has to be set where the step is, and the step is here.** `trace.Pin`
// applies to the calling thread and a step inherits its thread's affinity across
// the exec - which is how the guest pinned a step when the guest was what forked
// it. Under the shim it is this process that becomes the step, so this is the
// only place that can still do it, and without this the setting would go on
// being documented while doing nothing (E681, E685).
const EnvStepTracePin = "EARTH_STEP_TRACE_PIN"

// EnvStepUser is who the step runs as, as the Earthfile wrote it, and is empty
// for a step that keeps the guest's identity.
//
// **The names in it belong to the step, not to the guest.** `USER testuser`
// means whatever the step's own `/etc/passwd` says, which is why this is
// resolved in the shim after the chroot rather than anywhere earlier: at that
// point `/etc/passwd` *is* the step's, and a pure-Go lookup reads the right
// file without the guest having to parse it or agree with it.
const EnvStepUser = "EARTH_STEP_USER"

// EnvStepHome asks the shim to set HOME from the step user's passwd entry.
//
// **A flag rather than a value, because the two halves know different things.**
// The home directory is in the step's own `/etc/passwd`, which only exists as
// the step's after the chroot - so the shim reads it. Whether it *should* be
// read is a question about precedence: `stepEnv` folds a floor, what the image
// declared and ε into one environment, and afterwards a `HOME=/root` from the
// floor cannot be told from an image that meant it. Only the caller still has
// the layers apart, so only the caller can decide (E865a).
//
// Empty means leave HOME alone, which is what a step gets when its image or its
// Earthfile said something about it.
const EnvStepHome = "EARTH_STEP_HOME"

// EnvStepNetNS is the network namespace the step joins, as a path.
//
// Set by the guest under `EARTH_STEP_NET=private` and empty otherwise, so the
// shim's behaviour follows the guest's decision rather than reading the setting
// a second time and risking a different answer to the same question.
//
// A path rather than a name: the shim opens it and calls `setns`, and
// /var/run/netns/<name> is the bind mount that holds the namespace open in the
// first place. Joining rather than unsharing is the whole point - an unshared
// namespace would be empty and the step would have no way out.
const EnvStepNetNS = "EARTH_STEP_NETNS"

// stepShim is what the shim was asked to prepare and become.
type stepShim struct {
	// root is the filesystem the step sees, named from outside it: the chroot
	// has not happened yet when the shim reads this.
	root string
	// dir is the working directory, named from *inside* root, or empty for the
	// root itself.
	dir string
	// argv is the step's own command, keeping its own argv[0] - a shell reads
	// `$0`, and a step invoked under the shim's name is a step told a lie about
	// what it is.
	argv []string
}

// stepShimAsked reports what this argv asks the shim to do, or nil for an argv
// that is not asking.
//
// **Refused rather than guessed.** An argv too short to name a command would
// otherwise be run as something - and whatever that turned out to be would run
// as pid 1 of a namespace with the step's filesystem mounted under it.
func stepShimAsked(args []string) *stepShim {
	const (
		flag = 1
		root = 2
		dir  = 3
		cmd  = 4
	)

	if len(args) <= cmd || args[flag] != stepShimFlag {
		return nil
	}

	return &stepShim{root: args[root], dir: args[dir], argv: args[cmd:]}
}

// stepDir is the working directory the shim enters, named from inside the root.
//
// **Always absolute, because `chroot` does not change the working directory.**
// The shim is still standing where it started when it chroots, which is outside
// the root it has just entered - so a relative path resolves against that older
// place, naming a directory in the guest rather than in the step, and possibly
// one outside the new root altogether.
//
// The same rule is applied by the path this shim replaced, and `req.Dir` reaches
// both the same way. Rooting it also contains it: `Clean` folds any `..` against
// the leading separator rather than climbing out.
func stepDir(dir string) string { return filepath.Clean("/" + dir) }
