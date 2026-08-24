//go:build linux

package trace_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/trace"
)

// The marker that tells a re-executed test binary it is the helper.
const (
	helperEnv  = "EARTH_TEST_TRACE_HELPER"
	helperFD   = 3
	targetEnv  = "EARTH_TEST_TRACE_TARGET"
	programEnv = "EARTH_TEST_TRACE_PROGRAM"
)

// TestMain doubles as the helper a step is exec'd from.
//
// The same binary in two roles, which is how the fork-and-exec sequence gets
// tested without a second program to build and find: as a test it asserts, and
// under EARTH_TEST_TRACE_HELPER it installs the filter, hands the listener back
// and execs something real.
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "" {
		os.Exit(m.Run())
	}

	// Locked and never unlocked - the filter cannot come off this thread, and
	// the exec below replaces the process anyway.
	runtime.LockOSThread()

	listener, err := trace.InstallOnSelf()
	if err != nil {
		os.Stderr.WriteString("install: " + err.Error() + "\n")
		os.Exit(2)
	}

	conn, err := fdpass.ConnFromFD(helperFD)
	if err != nil {
		os.Stderr.WriteString("channel: " + err.Error() + "\n")
		os.Exit(3)
	}

	err = fdpass.SendFile(conn, listener)
	if err != nil {
		os.Stderr.WriteString("send: " + err.Error() + "\n")
		os.Exit(4)
	}

	// From here the thread is filtered and the reader on the other end is
	// answering. `cat` is a real program in a real process, exec'd over this
	// one: if the filter did not survive that, nothing below sees a thing.
	program := os.Getenv(programEnv)

	err = syscall.Exec(program, []string{program, os.Getenv(targetEnv)}, os.Environ())

	os.Stderr.WriteString("exec: " + err.Error() + "\n")
	os.Exit(5)
}

// The filter survives execve, so it is the *step* that is traced.
//
// This is the claim the whole design rests on and the one that cannot be
// inferred from the parts. Every test until now has filtered a thread of the
// engine and watched that thread; a step is a different program in a process
// that has replaced the one which installed the filter, and `PR_SET_NO_NEW_PRIVS`
// is what carries it across.
//
// `/bin/cat` rather than more Go: the step this engine runs is somebody else's
// program, and a test whose subject is the Go runtime opening its own files
// would prove the tracer sees Go.
func TestTheFilterSurvivesExecAndTracesTheStep(t *testing.T) {
	trace.SkipIfAlreadyFiltered(t)

	// Looked up rather than assumed: on NixOS there is no /bin/cat, and a test
	// that skipped for that reason would report "seccomp unavailable" on a
	// machine where the only thing missing was a conventional path.
	program, err := exec.LookPath("cat")
	if err != nil {
		t.Skipf("no cat to exec: %v", err)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "read-by-the-step-3d7c.txt")

	err = os.WriteFile(target, []byte("contents\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	here, there, err := fdpass.SocketPair()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = here.Close() }()

	channel, err := there.File()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = channel.Close(); _ = there.Close() }()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), self)
	cmd.Env = append(os.Environ(),
		helperEnv+"=1", targetEnv+"="+target, programEnv+"="+program)
	cmd.ExtraFiles = []*os.File{channel}
	cmd.Stdout, cmd.Stderr = nil, os.Stderr

	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	// **With a deadline, because the failure that matters does not fail.** The
	// skip below catches a helper that reports a problem. A helper that cannot
	// install a filter at all - no CAP_SYS_ADMIN, or already filtered by
	// something above - reports nothing and sends nothing, and this waits for a
	// descriptor that is not coming. On a hosted runner that is the whole
	// `go test -timeout 5m`, spent on one test, and it is what kept this
	// repository's own suite red (E587, E607).
	//
	// Ten seconds: the helper installs a filter and writes a descriptor, which
	// takes milliseconds when it works at all.
	err = here.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		t.Fatal(err)
	}

	listener, err := fdpass.RecvFile(here)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Skipf("the helper sent no listener, so seccomp is unavailable here: %v", err)
	}

	// Cleared, or every later read on this connection inherits it.
	err = here.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	tr := trace.NewTracer(int(listener.Fd()))

	done := make(chan struct{})

	go func() { tr.Run(); close(done) }()

	err = cmd.Wait()
	if err != nil {
		t.Fatalf("the exec'd step failed: %v", err)
	}

	// The step is gone, so the listener has no more to give and Run returns.
	<-done

	got := tr.Sightings()

	if !slices.Contains(got.Paths, target) {
		t.Errorf("the exec'd step read %q and the tracer did not see it"+
			"\n  saw %d paths, incomplete=%v %v"+
			"\n  the filter did not survive execve, or the step's own opens"+
			" are not reaching this listener",
			target, len(got.Paths), got.Incomplete, got.Why)
	}

	// And it saw the step's other business too - `cat` opens its libraries
	// before it opens its argument. A tracer seeing only the one path would be
	// seeing something other than a real process.
	if len(got.Paths) < 2 {
		t.Errorf("only %v; a real program opens more than its argument",
			got.Paths)
	}
}
