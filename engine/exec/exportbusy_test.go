package exec

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// An artifact can replace a binary that is running.
//
// `SAVE ARTIFACT` over a program the machine is executing is the ordinary case
// for a build that builds its own tool: CI builds `build/linux/amd64/earthly`
// with the copy of it that is running. Opening the destination for writing
// fails there with ETXTBSY - "text file busy" - which is not a condition the
// build can do anything about and is not a fault in the Earthfile.
//
// Replacing by rename also makes the export atomic: a reader sees the old file
// or the new one, and a build interrupted halfway leaves an artifact that is
// whole rather than truncated (E760).
func TestAnArtifactCanReplaceARunningBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dst := filepath.Join(dir, "tool")

	// A real binary, because only executing one takes the write lock that this
	// is about: a shell script's interpreter holds it open for reading, which
	// nothing objects to.
	// Looked up rather than assumed at /bin/sleep, which NixOS has not got:
	// /bin holds only sh there, and the first version of this test skipped
	// silently on the one machine that could have run it.
	from, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("no sleep on this machine to copy: %v", err)
	}

	binary, err := os.ReadFile(from)
	if err != nil {
		t.Skipf("cannot read %s: %v", from, err)
	}

	err = os.WriteFile(dst, binary, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	running := exec.Command(dst, "30")

	err = running.Start()
	if err != nil {
		t.Fatalf("start the binary that is to be replaced: %v", err)
	}

	defer func() {
		_ = running.Process.Kill()
		_, _ = running.Process.Wait()
	}()

	// **Waited for, not timed.** `Start` returns before `execve`, and the write
	// lock arrives with it - so the first version polled for a second and gave
	// up with `t.Skip`. Under `-race -shuffle=on` a loaded machine sometimes
	// took longer, the test skipped instead of running, and the skip ceiling
	// moved from 175 to 176 for a reason nobody could name from the log (E770).
	//
	// /proc/<pid>/exe resolves once the exec has happened, so this waits for
	// the condition itself rather than for a length of time.
	if runtime.GOOS == "linux" {
		waitForExec(t, running.Process.Pid, dst)
	} else {
		// Elsewhere there is no such file to consult, and macOS permits the
		// write anyway - so the honest thing is to say so rather than to assert
		// something this platform does not do.
		t.Skip("only linux takes a write lock on a running binary")
	}

	err = os.WriteFile(dst, binary, 0o755)
	if err == nil {
		t.Fatal("a running binary could be written to, so there is no lock to work around")
	}

	src := filepath.Join(dir, "new")

	err = os.WriteFile(src, append(binary, '\n'), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = copyOut(src, dst)
	if err != nil {
		t.Fatalf("an artifact could not replace a running binary: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(binary)+1 {
		t.Errorf("the destination is %d bytes, want the new artifact's %d",
			len(got), len(binary)+1)
	}
}

// waitForExec blocks until the process has actually exec'd the binary.
//
// The write lock a running binary carries is taken by `execve`, so a test that
// wants it has to wait for the exec and not for the fork. Deadlined rather than
// unbounded: a wait that hangs is worse than a test that fails.
func waitForExec(t *testing.T, pid int, want string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		at, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
		if err == nil && at == want {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("pid %d never exec'd %s", pid, want)
}
