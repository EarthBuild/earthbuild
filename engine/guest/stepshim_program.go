package guest

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultPath is where a bare program name is looked for when the step's
// environment declares no PATH.
//
// An image need not declare one, and Go's own lookup treats an empty PATH as
// "nowhere" rather than "the usual places" - so a step in such an image would
// fail to find `sh`. This is the list every container runtime falls back to, and
// it is a fallback only: a declared PATH is used exactly as declared.
const DefaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// resolveProgram reports whether a step's argv[0] can be exec'd as it stands.
//
// **The lookup itself happens in the guest, not here.** `lookIn` resolves a bare
// name against the step's PATH and root before the step is started; by the time
// the shim runs, its thread already carries the seccomp filter, and
// `trace.InstallOnSelf` states the contract for that window: keep it to the send
// and the exec. Stat-ing a dozen PATH entries in there is exactly what it says
// not to do.
//
// So all that is left is the message. `RUN ["python3", "--version"]` is the exec
// form - no shell, so nothing else resolves the name - and a bare name that
// reached this point is one the guest could not find anywhere on the step's
// PATH. `syscall.Exec` would report `no such file or directory` against a name
// with no path in it, which sends the reader looking for a file rather than for
// a missing program.
func resolveProgram(name string) error {
	if name == "" {
		return errors.New("a step has no command to run")
	}

	if strings.Contains(name, "/") {
		return nil
	}

	return fmt.Errorf("%s is not on this step's PATH"+
		"\n  the exec form runs no shell, so the name is looked up as written"+
		"\n  give the path, or use the shell form", name)
}
