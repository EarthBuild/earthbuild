package exec

import (
	"os"
	"os/exec"
	"path/filepath"
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

	// Started is not running: the kernel takes the write lock at execve, and
	// Start returns before that has happened.
	for range 100 {
		if err = os.WriteFile(dst, binary, 0o755); err != nil {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	if err == nil {
		t.Skip("this kernel let a running binary be written to; nothing to prove")
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
