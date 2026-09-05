package guest

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestACopiedFileKeepsItsModeWhateverTheUmask.
//
// **A creation mode is a request, and `umask` is the answer.**
// `os.OpenFile(dst, O_CREATE, 0o777)` under the ordinary umask of 022 makes a
// file 0755, so a step that ran `chmod 777 f` had its layer captured with the
// file at 755 and the next step read 755. Measured end to end: 777 out of one
// step, 755 into the next.
//
// **The determinism is the worse half.** A mode is part of a layer (I8), so the
// layer this engine produces depended on the umask of whoever invoked it - two
// machines, two layers, two keys, for the same build. That is environment
// leaking into identity, which is the thing a content-addressed store exists to
// prevent.
//
// `chmod` after creating is the fix, because `chmod` is not masked. Setting the
// process umask to 0 instead would work and is worse: it is global, it affects
// every other file this process writes, and it would leave the same trap for
// the next person who adds an `OpenFile`.
func TestACopiedFileKeepsItsModeWhateverTheUmask(t *testing.T) { //nolint:paralleltest // umask is per-process state
	// Not parallel, and the linter is told why: umask belongs to the process,
	// so a parallel test would set it under another one.
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	root := t.TempDir()

	src := filepath.Join(root, "src")

	err := os.WriteFile(src, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// 0777 is the subject, not a lapse: the point is that a mode the umask
	// would trim survives the copy.
	err = os.Chmod(src, 0o777) //nolint:gosec // the mode under test
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(root, "dst")

	err = copyFile(src, dst, 0o777)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}

	if got := fi.Mode().Perm(); got != 0o777 {
		t.Errorf("the copy has mode %04o under umask 022, want 0777"+
			"\n  a creation mode is masked, so the layer this build produces"+
			" depends on who invoked it", got)
	}
}
