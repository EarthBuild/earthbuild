package guest

import "os"

// EnvStepShim launches a step through the shim, so that its `/proc` describes
// the step rather than the guest.
//
// **Behind a setting while it earns its place.** The launch is the most delicate
// call in the engine - a chroot, four namespaces and a cgroup at clone time -
// and this changes who performs the chroot. The default path is untouched, so
// turning it off is turning it off, and the two can be compared on one machine.
//
// What it buys: a step runs in a PID namespace of its own and reads `$$` as 1,
// while a `/proc` mounted by the guest before the clone answers with the guest's
// numbering. Anything consulting `/proc/$$` lands on another process (E705).
//
// What it costs: one extra exec per step, measured at 2.2ms - about 15ms of a
// 41s cold `+earthly`. And the shim runs inside the traced step, so until the
// tracer is taught that a step's observations begin at its own `execve`, the
// shim's own startup is observed along with the step's.
const EnvStepShim = "EARTH_STEP_SHIM"

// stepShimWanted reports whether steps should be launched through the shim.
func stepShimWanted() bool { return os.Getenv(EnvStepShim) != "" }
