package exec

// guestStore is where the layer store appears inside the sandbox. Fixed, so
// that nothing has to derive it from a host path.
//
// Here rather than beside the backend that mounts it, because it is a fact
// about the contract between host and guest and not about any one platform's
// way of arranging a sandbox. It was in the darwin file until something
// platform-independent needed it, and the compiler said so on the other
// platform rather than at the point of the mistake.
const guestStore = "/var/lib/earthbuild/store"
