package guest

import "os"

// EnvOwnsMachine says this guest is the only reason its machine is running.
//
// **The idle timeout stopped the agent and not the machine.** A sandbox that
// nobody has used for half an hour exits, which is the whole point of `idle` -
// and in a VM the agent is not what holds the machine open. The runtime starts
// it with a keep-alive as PID 1, so the guest exits, the VM keeps running with
// a `sleep` in it, and the memory stays reserved until that sleep ends a day
// later. Twenty-six of them were found on one developer's machine (E555).
//
// Set by a backend that starts a machine of its own and holds it open. Not set
// by one that confines with namespaces, where PID 1 is the *host's* init and
// signalling it would be a considerably worse bug than the one being fixed.
//
// A grant rather than a discovery: the guest cannot tell from inside whether
// the process at PID 1 is a keep-alive the engine started or something it must
// never touch, so it is told, and it checks as well (see stopMachine).
const EnvOwnsMachine = "EARTH_GUEST_OWNS_MACHINE"

// OwnsMachine reports whether this guest was granted the right to stop the
// machine it is running in.
func OwnsMachine() bool { return os.Getenv(EnvOwnsMachine) != "" }
