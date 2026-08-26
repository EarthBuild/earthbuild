package guest

// stepShimFlag marks this process as the shim that prepares a step.
//
// Distinct from daemonShimFlag because the two prepare different things for
// different children, and a process that answered to both would be one flag away
// from mounting a daemon's `/run` over a step's filesystem.
const stepShimFlag = "--earthbuild-step-shim"

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
