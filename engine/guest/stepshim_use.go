package guest

import "os"

// EnvStepShim launches a step through the shim, so that its `/proc` describes
// the step rather than the guest. `0` turns it off.
//
// **On, because the arrangement without it is wrong.** A step runs in a PID
// namespace of its own and reads `$$` as 1, while a `/proc` mounted by the guest
// before the clone answers with the guest's numbering - so the step disagrees
// with itself and anything consulting `/proc/$$` lands on another process
// (E705). The reference engine is self-consistent here; this is what makes this
// one so.
//
// What it costs is one extra exec per step, measured at 2.2ms, which is about
// 15ms of a 44s cold `+earthly` - some five hundred times smaller than the
// run-to-run spread of that build, and so not measurable end to end.
//
// The switch remains because the launch is the most delicate call in the engine
// - a chroot, four namespaces and a cgroup at clone time - and this changes who
// performs the chroot. An operator who suspects it can turn it off and compare
// on one machine.
const EnvStepShim = "EARTH_STEP_SHIM"

// StepShimWanted reports whether steps are launched through the shim.
//
// Exported because the sandbox's name has to carry the *effective* answer rather
// than the raw setting: a default that changes must change the name, or a
// machine started under the old one keeps serving it and the change reads as
// having done nothing (E549, E682, E701).
func StepShimWanted() bool { return os.Getenv(EnvStepShim) != "0" }

// stepShimWanted is StepShimWanted, for callers inside this package.
func stepShimWanted() bool { return StepShimWanted() }
