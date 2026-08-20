package exec_test

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// A loopback connection takes its scratch directory away with it.
//
// `LoopbackConn` makes one to hold the guest's filesystem and closed only the
// pipes, so every call left a directory behind. Found on the x86 box, where
// `/tmp` held 2890 of them and the root filesystem was full - which stopped the
// run gate rather than any test that was watching for this (E473).
//
// **A cleanup attached to the wrong lifetime is not a cleanup.** The pipes were
// closed carefully and the directory they were made for was not, because Close
// knew about one and not the other.
func TestALoopbackConnectionTakesItsScratchWithIt(t *testing.T) {
	t.Parallel()

	c := exec.LoopbackConn()

	// The connection's own directory, asked of the connection.
	//
	// The first version globbed `/tmp` and compared counts before and after,
	// which is every other test's litter as well as this one's: the package
	// dials loopbacks from two more places, so the count moved for reasons this
	// test knows nothing about and it failed inside the package run while
	// passing alone. *An observable wider than the thing being observed reports
	// other people's news.*
	named, ok := c.(interface{ Scratch() string })
	if !ok {
		t.Fatal("a loopback connection cannot say where it scratches, so this" +
			" test cannot tell its own directory from anyone else's")
	}

	dir := named.Scratch()
	if dir == "" {
		t.Fatal("a loopback connection owns no directory, so either it makes" +
			" none or it does not know it owns one")
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the directory the guest lives in is not there: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%s outlived the connection that made it (%v)"+
			"\n  a temporary that outlives its owner is not temporary", dir, err)
	}
}
