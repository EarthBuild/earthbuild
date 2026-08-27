package exec

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	// **This test's own binary, because it is the one thing certainly runnable
	// here.** Only executing a real binary takes the write lock this is about -
	// a shell script's interpreter holds it open for reading, which nothing
	// objects to. The first version copied `sleep`, and in the CI container the
	// copy exited 127, so the test skipped and the skip ceiling moved (E770).
	// Whatever that copy was missing, the process running this test is not
	// missing it.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(dst, binary, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	// The helper-process idiom: the copy is this test binary, so it is asked to
	// run one test that does nothing but wait.
	running := exec.Command(dst)
	running.Env = append(os.Environ(), sleepHelperEnv+"=1")

	err = running.Start()
	if err != nil {
		t.Fatalf("start the binary that is to be replaced: %v", err)
	}

	defer func() {
		_ = running.Process.Kill()
		_, _ = running.Process.Wait()
	}()

	// **Waited for, not timed - and the condition is the lock itself.**
	// `Start` returns before `execve`, and the write lock arrives with it, so
	// the first version polled for a second and gave up with `t.Skip`: under
	// `-race -shuffle=on` a loaded runner sometimes took longer, the test
	// skipped rather than ran, and the skip ceiling moved for a reason nobody
	// could name (E770).
	//
	// The second version waited on `/proc/<pid>/exe`, which is a *proxy* for
	// the lock, and it never matched in the CI container - so the test failed
	// after thirty seconds having proven nothing. Waiting for the write to be
	// refused is the same wait without the proxy, and it is the thing the test
	// is about.
	if runtime.GOOS != "linux" {
		// macOS permits writing to a running binary, so there is no lock here
		// to work around and nothing this test could assert.
		t.Skip("only linux takes a write lock on a running binary")
	}

	// **Three outcomes, and only one of them is this test's business.** The
	// write is refused, which is the case under test; or the copy never runs at
	// all, which is a filesystem mounted `noexec` and nothing about the engine;
	// or neither happens and something is wrong with the wait.
	exited := make(chan error, 1)

	go func() { exited <- running.Wait() }()

	deadline := time.Now().Add(30 * time.Second)

	for err == nil && time.Now().Before(deadline) {
		select {
		case waitErr := <-exited:
			t.Skipf("the copied binary did not stay running (%v), so nothing"+
				" here holds a write lock - a temporary directory mounted"+
				" noexec does this", waitErr)
		default:
		}

		err = os.WriteFile(dst, binary, 0o755)
		if err == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}

	if err == nil {
		t.Fatal("thirty seconds of a running binary that stayed writable: the" +
			" process is alive and this kernel took no write lock, which is the" +
			" one outcome that would make the fix pointless")
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

// sleepHelperEnv makes the test binary wait instead of running the suite.
//
// Read by TestMain in helpers_test.go, which is the external test package and
// cannot see this constant - so the string is written out there too, and in no
// third place.
const sleepHelperEnv = "EARTH_TEST_SLEEP_HELPER"
