package ir

// OnInvokerOnly reports whether an operation can run only on the machine that
// started the build.
//
// **One list, two readers.** The fleet refuses to delegate these
// (`ErrNotDelegable`) and the scheduler must not place them elsewhere; those are
// a guarantee and a model of the same fact, and they were written separately.
// The scheduler knew about one of the four, so a worker was charged for work it
// would refuse and the invoker was not charged for work it would do - on every
// build using a secret, a docker daemon or a terminal (E426, E430).
//
// Here rather than in either caller because both already depend on this package
// and neither depends on the other. A fifth entry added here reaches both; one
// added to only one of them is the drift this exists to prevent.
func (o Op) OnInvokerOnly() (bool, string) {
	switch {
	case o.Kind == OpHost:
		return true, "it runs on the invoking machine"

	case o.Kind == OpLocal:
		return true, "it reads the invoking machine's filesystem"

	case len(o.SecretEnv) > 0:
		return true, "it needs a secret, which an assignment does not carry"

	case pinning(o.Mounts) != "":
		return true, "it needs " + pinning(o.Mounts) + ", whose contents live on this machine"

	case o.Docker:
		return true, "it needs a docker daemon, which the assignment does not describe"

	case o.Interactive:
		return true, "it needs a terminal, and the person holding one is here"
	}

	return false, ""
}

// pinning names the first mount that only the invoking machine can provide.
//
// A *named* cache is a directory on this machine, and a worker given the step
// would run it against an empty directory it believes is warm - a wrong build,
// not a slow one. A secret is a value the assignment does not carry, whatever
// else is true of the mount.
//
// `--sharing=private` is neither: the guest makes the directory for the step and
// removes it after (§3.3c), so every machine produces the same one - empty. The
// rule was "any mount pins", which for a cargo or npm build meant nothing was
// ever delegated, because those put a CACHE in almost every RUN (E433).
//
// Returns the mount's name rather than a bool, because the scheduler prints this
// and "a cache mount" leaves the author to work out which of five it was.
func pinning(mounts []Mount) string {
	for _, m := range mounts {
		// Portable means exactly this and nothing else. Written as a comparison
		// against a constructed mount rather than as a list of the fields that
		// disqualify one, so that a field added to `Mount` pins the step until
		// somebody decides otherwise - refusing to delegate something we could
		// have costs a slower build, and delegating something we could not costs
		// a wrong one.
		if m == (Mount{Target: m.Target, Ephemeral: true}) {
			continue
		}

		switch {
		case m.Secret:
			return "a secret at " + m.Target

		case m.ID != "":
			return "the cache " + m.ID

		default:
			return "the cache at " + m.Target
		}
	}

	return ""
}
