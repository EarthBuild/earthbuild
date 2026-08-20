package overlay

// mountPrefix names a step's mount directory, which the filesystem completes.
//
// **Asked for rather than derived.** The name used to be `h<pid>-<counter>`,
// with the pid distinguishing this process's mounts from a dead one's: a killed
// guest leaves its mounts behind, so the next guest asking for `h000001` finds
// them and overlayfs answers EBUSY. That reasoning was right about the dead and
// silent about the living.
//
// On Linux the guest runs in a PID namespace, so `os.Getpid()` is **1 for every
// guest there has ever been**. Two builds sharing a store each asked for
// `h1-000001`, landed in one overlay, and both steps wrote into one upper
// directory - two builds that both succeeded and both produced the other's
// output (E140).
//
// `MkdirTemp` is the only party that can promise a name nobody has, and a name
// nobody has is also a name no corpse is holding: it subsumes the case the pid
// was there for.
const mountPrefix = "h-"
