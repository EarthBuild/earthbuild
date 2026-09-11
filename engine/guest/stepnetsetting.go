package guest

// EnvStepNet selects how a step reaches the network. See docs/native/settings.md.
//
// `private` is the default: each step gets a namespace of its own with a way
// out. `shared` gives every step the guest's own namespace, which is what builds
// did before this existed and is the answer where a backend's virtual NIC will
// not carry a second MAC.
//
// **Not in the Linux-only file it started in.** The behaviour is Linux's, but
// the *name* belongs to whoever launches a guest - and a backend that cannot see
// the constant cannot forward the setting, which is how it came to be silently
// ignored on macOS. See TestEveryGuestSettingIsForwardedOrExcused.
const EnvStepNet = "EARTH_STEP_NET"

// The two values EnvStepNet takes.
const (
	NetShared  = "shared"
	NetPrivate = "private"
)

// EnvStepLink selects how a step's interface hangs off the guest's own.
//
// `macvlan` gives the child its own MAC and is the better arrangement where
// anything will carry it. `ipvlan` shares the parent's MAC, which is what gets
// past a virtual NIC that forwards one MAC and drops the rest.
const EnvStepLink = "EARTH_STEP_LINK"
