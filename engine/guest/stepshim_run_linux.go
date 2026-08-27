//go:build linux

package guest

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/trace"
)

// RunStepShimIfAsked turns this process into a step shim when its argv says so,
// and never returns if it does.
//
// Called first thing in `main`, by every binary that can host a guest, and from
// the test binary's `TestMain` - the daemon shim learned that the hard way, when
// a launch re-executed the tests instead of a daemon and every assertion about
// stopping it passed while measuring an absence (E374).
//
// Go cannot run code between clone and exec, so the step's namespaces are
// entered by re-executing this binary with the flags on `SysProcAttr`, and the
// preparation happens here, in the child, before the step replaces it. The shim
// chroots itself rather than letting `SysProcAttr.Chroot` do it, which is what
// lets it be the guest's own binary at the guest's own path: nothing is written
// into the step's filesystem and nothing needs undoing before the layer is
// captured (E705).
func RunStepShimIfAsked() {
	sh := stepShimAsked(os.Args)
	if sh == nil {
		return
	}

	fail := func(err error) {
		fmt.Fprintf(os.Stderr, "earthbuild step shim: %v\n", err)
		os.Exit(1)
	}

	err := prepareStep(sh)
	if err != nil {
		fail(err)
	}

	err = enterStep(sh)
	if err != nil {
		fail(err)
	}

	// Exec, not run: the step becomes this process, so the guest's Wait sees the
	// step's own exit and a signal reaches the step rather than a wrapper that
	// would have to forward it. It also means the shim is gone by the time the
	// step's first instruction runs, which is what keeps it out of the step's
	// process table.
	//
	// G204: the argv is the step's, which is the whole job.
	err = resolveProgram(sh.argv[0])
	if err != nil {
		fail(err)
	}

	// **Last, and after every path this shim touches.** From here the thread
	// carries a filter whose only answerer is the guest, so a traced syscall
	// made before the listener reaches it would stop with nobody to answer -
	// which is why `prepareStep`, `enterStep` and `resolveProgram` are all
	// above this line, and why the program lookup happens in the guest.
	err = handOverTracing()
	if err != nil {
		fail(err)
	}

	err = syscall.Exec(sh.argv[0], sh.argv, stepEnviron()) //nolint:gosec // see above

	fail(fmt.Errorf("exec %s: %w", sh.argv[0], err))
}

// handOverTracing installs the step's seccomp filter and sends the listener to
// the guest, or does nothing at all for a step nobody is watching.
//
// The sequence is `trace.InstallOnSelf`'s and is exact: lock the thread, install,
// send, exec. Nothing else belongs between the install and the exec - see
// EnvStepTraceFD for why the install is here rather than in the guest.
func handOverTracing() error {
	name := os.Getenv(EnvStepTraceFD)
	if name == "" {
		return nil
	}

	fd, err := strconv.Atoi(name)
	if err != nil {
		return fmt.Errorf("%s is %q, which is not a descriptor: %w", EnvStepTraceFD, name, err)
	}

	// Locked and never unlocked. A seccomp filter cannot be removed, so the
	// thread carrying one has to be destroyed rather than handed back - and
	// here it is not destroyed but *becomes the step*, which is the arrangement
	// this whole path exists for.
	runtime.LockOSThread()

	conn, err := fdpass.ConnFromFD(fd)
	if err != nil {
		return fmt.Errorf("open the guest's channel on fd %d: %w", fd, err)
	}

	listener, err := trace.InstallOnSelf()
	if err != nil {
		// **An unobservable step still runs.** Tracing is how a step earns an
		// L2 hit, not how it is allowed to execute, so a filter that will not
		// install costs the tier and nothing else (I3, I11) - the arrangement
		// before the shim said so and this keeps saying it.
		//
		// **Answered rather than left silent, and answered with a byte.** The
		// guest is blocked reading this channel. Closing is not enough to end
		// that read: the guest holds its own copy of the step's end until the
		// step is over, so no end-of-file arrives while it waits. A message
		// carrying no descriptor does arrive, and `fdpass.RecvFile` reports it
		// at once as a message with no rights in it.
		//
		// Without this every step in an environment that cannot install a
		// filter - a container without CAP_SYS_ADMIN, say - would pay the whole
		// listener deadline before running.
		_, _ = conn.Write([]byte{0})
		_ = conn.Close()

		return nil
	}

	err = fdpass.SendFile(conn, listener)
	if err != nil {
		return fmt.Errorf("hand the syscall listener to the guest: %w", err)
	}

	// Best effort, and after the install so that a failure to pin cannot leave
	// a step running without the filter it was promised. A step that could not
	// be pinned runs at the speed it ran at before pinning existed, which is not
	// a failure worth refusing a build over.
	if cpu, err := strconv.Atoi(os.Getenv(EnvStepTracePin)); err == nil {
		_ = trace.Pin(cpu)
	}

	// **Not inherited by the step.** A listener left open across the exec is a
	// descriptor on which the step could answer its own notifications, and so
	// decide for itself what this engine records about it. The guest holds the
	// copy that matters - SCM_RIGHTS transferred it at the send - so letting go
	// here costs nothing.
	unix.CloseOnExec(int(listener.Fd()))

	// The channel likewise, and by closing rather than by flag: `ConnFromFD`
	// duplicates the descriptor it is given and closes the original, so the
	// number this function was handed is already shut and the live one is
	// inside the connection. Data already sent is still delivered.
	_ = conn.Close()

	return nil
}

// stepEnviron is this process's environment with the engine's own variables
// taken out of it.
//
// **A step must not be able to see how it is being run.** EnvStepTraceFD is
// addressed to the shim and names a descriptor that is closed by the time the
// step starts, so passing it on would be meaningless as well as untidy - and it
// would make a step's environment differ depending on whether the shim was in
// use, which is a difference no Earthfile asked for and one that a step reading
// `env` can see.
func stepEnviron() []string {
	all := os.Environ()
	out := make([]string, 0, len(all))

	for _, kv := range all {
		if strings.HasPrefix(kv, EnvStepTraceFD+"=") ||
			strings.HasPrefix(kv, EnvStepTracePin+"=") {
			continue
		}

		out = append(out, kv)
	}

	return out
}
