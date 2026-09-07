package guest

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A step that fails saying nothing hands over everything else that is known.
//
// **Because "exited 2, and printed nothing" is the whole of what a reader gets,
// and it is not enough to act on.** It cost an afternoon: the command was
// reproduced in isolation, run sixteen ways in parallel, checked for lost
// output and for crossed streams - all because the one line said nothing about
// *how* it failed. Everything below was already in hand at the moment of
// failure and was thrown away.
func TestASilentFailureSaysWhatElseIsKnown(t *testing.T) {
	t.Parallel()

	got := silentNote(failure{
		exit: 2,
		cpu:  1500 * time.Millisecond,
		rss:  512 << 20,
		ran:  9 * time.Second,
	})

	for _, want := range []string{"1.5s", "512", "9s"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note does not mention %q:\n%s", want, got)
		}
	}
}

// A signal is named rather than left as a number.
//
// Go reports a signalled process as exit -1, so the number alone says "this did
// not exit at all" and nothing about why. `killed by SIGKILL` is what a reader
// needs; on this path it is usually the memory limit.
func TestASignalIsNamed(t *testing.T) {
	t.Parallel()

	got := silentNote(failure{exit: -1, signal: syscall.SIGKILL})

	if !strings.Contains(got, "SIGKILL") {
		t.Errorf("a signalled step is described as %q", got)
	}
}

// An out-of-memory kill is stated outright, because it is the one cause a
// reader cannot infer from anything else the step left behind.
func TestAnOOMKillIsStated(t *testing.T) {
	t.Parallel()

	got := silentNote(failure{exit: 137, oomKills: 1})

	if !strings.Contains(strings.ToLower(got), "out of memory") {
		t.Errorf("an OOM kill is described as %q", got)
	}
}

// A step that printed something needs no note: it has already said more than
// this could.
func TestAStepThatSpokeGetsNoNote(t *testing.T) {
	t.Parallel()

	if got := noteFor([]byte("something"), failure{exit: 1}); got != "" {
		t.Errorf("a step that printed output was also given a note: %q", got)
	}
}

// The cgroup's own count, read from where the kernel writes it.
func TestOOMKillsAreReadFromTheCgroup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(dir+"/memory.events",
		[]byte("low 0\nhigh 0\nmax 3\noom 2\noom_kill 1\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got := oomKillsIn(dir); got != 1 {
		t.Errorf("read %d oom kills, wanted 1", got)
	}
}
