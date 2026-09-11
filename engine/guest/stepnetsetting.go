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
