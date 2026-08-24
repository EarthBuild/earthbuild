//go:build linux

package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// The tracer is given the filler, so a step can open what is not there yet.
//
// **This is the whole of lazy materialisation from the guest's side.** The step
// opens a path, the tracer stops the syscall, the filler fetches the file, and
// the open proceeds - so a base need not be assembled whole before a step that
// reads a tenth of it can start (E296).
//
// A tracer with no filler watches instead of filling. Nothing fails: the step
// gets its honest ENOENT, takes whatever branch that implies, and the build
// succeeds having quietly stopped being lazy. The comment beside the line even
// says the filler is "nil for every build today", which is why the wiring is
// easy to lose and hard to miss.
//
// Removing the line left the package green.
func TestTheTracerIsHandedTheFiller(t *testing.T) {
	// Not parallel: it installs a filter on a thread of its own.
	root := t.TempDir()
	absent := filepath.Join(root, "not-here-yet")

	var (
		mu     sync.Mutex
		asked  []string
		filled bool
	)

	fill := func(path string) error {
		mu.Lock()
		defer mu.Unlock()

		asked = append(asked, path)

		// What a real filler does: put the file there, so the syscall the step
		// is stopped in can go on and find it.
		if path == absent {
			filled = true

			return os.WriteFile(path, []byte("fetched"), 0o600)
		}

		return nil
	}

	// **A subprocess, because that is what a step is.** `StartOnSelf` installs
	// the filter on the calling thread and then disregards that thread's own
	// syscalls - the engine goes on working on it, and its reads are not the
	// step's (E211). What makes the step observed is that it forks: a seccomp
	// filter is inherited across fork and carried through exec, so the child
	// traps where the parent does not.
	//
	// Reading the file in-process here observed nothing at all, which is correct
	// behaviour and a useless test.
	step := func() ([]byte, error) {
		return exec.Command("/bin/sh", "-c", "cat "+absent).CombinedOutput()
	}

	out, seen, err := runObserved(step, fill, func() {})

	t.Logf("sightings: paths=%v incomplete=%v why=%v", seen.Paths, seen.Incomplete, seen.Why)

	mu.Lock()
	defer mu.Unlock()

	if !filled {
		t.Fatalf("the step opened %s and the filler was never asked for it"+
			"\n  a tracer with no filler watches instead of filling, and the"+
			" step takes the absent branch while the build reports success"+
			" (E296)\n  asked for: %v", absent, asked)
	}

	if err != nil {
		t.Errorf("the step failed although the file was fetched: %v", err)
	}

	if string(out) != "fetched" {
		t.Errorf("the step read %q, want the fetched contents", out)
	}
}
