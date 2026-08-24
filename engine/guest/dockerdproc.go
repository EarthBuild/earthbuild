package guest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// gracePeriod is how long a daemon is given to shut down before it is killed.
//
// `dockerd` stopping a container of its own takes a moment, and killing it
// mid-flight leaves the storage area in whatever state it was in - which for a
// named cache is a directory the next build will read. Two seconds is enough for
// an idle daemon, and the kill is there for the one that is not.
const gracePeriod = 2 * time.Second

// dockerd is a launched daemon that is not yet known to be answering.
type dockerd struct {
	cmd  *osexec.Cmd
	sock string
	// done is closed when the process has been reaped, and gone holds why.
	//
	// A closed channel rather than a value on one, because two places read this:
	// Ask, on every poll of the wait, and Stop, which must not return before the
	// process is gone. A single value is delivered once, so whichever arrived
	// first would consume it and the other would wait for something that had
	// already happened.
	//
	// *A one-shot signal read from two places.*
	done chan struct{}
	gone error
	// says keeps the tail of what the daemon wrote, so its own explanation
	// reaches the author rather than the guest's log.
	says  *tail
	stop  sync.Once
	after error
}

// lookFn finds a program, so a test can supply one.
type lookFn func(string) (string, error)

// launchDockerd starts the guest's own dockerd with the given arguments.
func launchDockerd(ctx context.Context, argv []string, sock string) (daemonProcess, error) {
	return launchWith(ctx, osexec.LookPath, argv, sock)
}

// launchWith is launchDockerd with the lookup made explicit.
//
// The refusal names the *guest*, deliberately. Every message about an
// unreachable daemon suggests installing Docker in the image, and here that is
// exactly the wrong advice: the daemon runs beside the step (E368), so the image
// needs a client and the machine needs the daemon.
func launchWith(
	ctx context.Context, look lookFn, argv []string, sock string,
) (daemonProcess, error) {
	bin, err := look("dockerd")
	if err != nil {
		return nil, fmt.Errorf(
			"this step asked for a daemon and the guest has no dockerd on its PATH"+
				"\n  the daemon runs beside the step, not inside it, so this is the"+
				"\n  machine's dockerd and not the base image's: %w", err)
	}

	// This binary, not `dockerd` - see RunDaemonShimIfAsked. `dockerd` refuses to
	// start unless it is root and has a writable `/run`, and Go cannot run code
	// between clone and exec, so the namespace is entered by re-executing
	// ourselves and the mount is done in the child (E373).
	self, err := selfExe()
	if err != nil {
		return nil, fmt.Errorf("find this binary, to run the daemon shim: %w", err)
	}

	// Not CommandContext: the context ends the *step*, and a daemon killed by it
	// would die before the deferred Stop can shut it down cleanly. Stop is the
	// only thing that ends this process, and withDaemon calls it on every path.
	cmd := osexec.Command(self, shimArgv(bin, argv)...) //nolint:gosec // argv is this package's
	// Kept as well as forwarded. A daemon that will not start says why, and every
	// such message in this project has been the answer - `needs to be started
	// with root privileges`, `mkdir /run/docker/plugins`, `unix socket path too
	// long`. Written only to the guest's stderr they land in a log nobody reads,
	// and the author gets `exit status 1` (E379).
	said := &tail{}
	cmd.Stdout, cmd.Stderr = io.MultiWriter(os.Stderr, said), io.MultiWriter(os.Stderr, said)

	// Its own group, so Stop reaches whatever it spawned. A daemon leaves
	// containerd-shims behind, and a signal to the leader alone leaves them
	// holding the step's filesystem open.
	cmd.SysProcAttr = namespaced(&syscall.SysProcAttr{})
	ownGroup(cmd)

	err = cmd.Start()
	if err != nil {
		// The hint here as well as at the mount: in a plain container the
		// refusal arrives at `clone`, so the shim never runs and a hint written
		// inside it is a hint nobody reaches (E387).
		return nil, fmt.Errorf("start %s: %w%s", bin, err, sysAdminHint(err))
	}

	// The socket it owns, said rather than parsed back out of its own argv: a
	// daemon asked on the client's default socket answers with the *machine's*
	// daemon, so the wait passes before the step's has bound anything (E378).
	d := &dockerd{cmd: cmd, sock: sock, says: said, done: make(chan struct{})}

	go func() {
		d.gone = errOr(cmd.Wait())
		close(d.done)
	}()

	return d, nil
}

// Ask puts a question to the daemon that only a running server can answer.
//
// The version, not the driver: `docker info --format '{{.Driver}}'` renders
// empty and exits zero against no server at all (E364), and the wait above
// refuses an empty answer for that reason.
func (d *dockerd) Ask(ctx context.Context) (string, error) {
	// A process that has already exited is answered immediately. Otherwise a
	// dockerd that refuses its own flags costs every step the whole timeout
	// before the author is told anything at all.
	err := d.exited()
	if err != nil {
		return "", err
	}

	out, err := osexec.CommandContext(ctx, "docker", "-H", "unix://"+d.sock,
		"info", "--format", "{{.ServerVersion}} {{.Driver}}").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}

	return strings.TrimSpace(string(out)), nil
}

// exited reports the daemon's own exit, once it has one.
func (d *dockerd) exited() error {
	select {
	case <-d.done:
		if said := d.says.String(); said != "" {
			return fmt.Errorf("the daemon exited before it answered: %w\n  it said: %s",
				d.gone, said)
		}

		return fmt.Errorf("the daemon exited before it answered: %w", d.gone)
	default:
		return nil
	}
}

// errOr names a clean exit, which is still a failure here: a daemon that returns
// zero has stopped serving just as surely as one that crashed.
func errOr(err error) error {
	if err == nil {
		return errors.New("exit status 0")
	}

	return err
}

// Stop ends the daemon, and does not return until it is gone.
//
// SIGTERM to the group first, because a daemon stopping a container of its own
// needs a moment and killing it mid-flight leaves a named cache's storage in
// whatever state it was in. SIGKILL after the grace period, because "it would
// not stop" must not mean "it is still running when the capture reads the step's
// filesystem".
func (d *dockerd) Stop() error {
	d.stop.Do(func() {
		pgid := -d.cmd.Process.Pid

		_ = killGroup(pgid, syscall.SIGTERM)

		// A daemon that is already gone falls straight through here, because
		// done is closed rather than delivered - which is also why no separate
		// early return is needed above. The mutation sweep proved that: deleting
		// one survived every test, because the latch had already done its work.
		select {
		case <-d.done:
			return
		case <-time.After(gracePeriod):
		}

		err := killGroup(pgid, syscall.SIGKILL)
		if err != nil {
			d.after = fmt.Errorf("kill the step's daemon: %w", err)

			return
		}

		<-d.done
	})

	return d.after
}
