package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// A program on PATH is found when it is an absolute symlink inside the root.
//
// **`/usr/bin/python3 -> /usr/bin/python3.13` is how Debian ships it**, and
// distroless with it. The link is absolute, so following it from outside the
// step means following it against the *guest's* root, where nothing of the
// step's exists - so the lookup reported "python3 is not on this step's PATH"
// for a program sitting right there, and `RUN ["python3", "--version"]` failed
// on an image whose whole purpose is to run python.
//
// The exec form is where it bites, because there is no shell to do the lookup
// instead: the shell form works, and `RUN ["/usr/bin/python3", ...]` works, and
// only the portable spelling fails.
func TestAProgramIsFoundBehindAnAbsoluteSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	bin := filepath.Join(root, "usr", "bin")

	err := os.MkdirAll(bin, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	// The real program, and the name the image puts on PATH pointing at it by
	// an absolute path - absolute *inside the image*, which is the whole point.
	program := filepath.Join(bin, "python3.13")

	err = os.WriteFile(program, []byte("#!/bin/true\n"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("/usr/bin/python3.13", filepath.Join(bin, "python3"))
	if err != nil {
		t.Fatal(err)
	}

	got := lookIn(root, "python3", []string{"PATH=/usr/bin"})

	if got != "/usr/bin/python3" {
		t.Errorf("lookIn found %q, want \"/usr/bin/python3\""+
			"\n  the entry is a symlink whose target is absolute *within the"+
			" step's root*; resolving it against the guest's root finds nothing"+
			" and reports a program that is present as missing", got)
	}

	// A relative link, which already worked, must go on working.
	err = os.Symlink("python3.13", filepath.Join(bin, "python3rel"))
	if err != nil {
		t.Fatal(err)
	}

	if got := lookIn(root, "python3rel", []string{"PATH=/usr/bin"}); got != "/usr/bin/python3rel" {
		t.Errorf("a relative link resolved to %q, want \"/usr/bin/python3rel\"", got)
	}

	// A link to nothing is still not a program.
	err = os.Symlink("/usr/bin/absent", filepath.Join(bin, "dangling"))
	if err != nil {
		t.Fatal(err)
	}

	if got := lookIn(root, "dangling", []string{"PATH=/usr/bin"}); got != "dangling" {
		t.Errorf("a dangling link resolved to %q, want the name unchanged"+
			"\n  nothing on PATH matches, so the failure should be the kernel's,"+
			" naming what was asked for", got)
	}
}
