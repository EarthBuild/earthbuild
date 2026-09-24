package guest

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/internal/sourceguard"
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

// **A step that was killed cannot have said so itself.**
//
// The note is otherwise suppressed once a step has printed anything, on the
// grounds that its own output says more than this could. That holds for an
// ordinary failure and not for these two: a process killed for memory prints
// `Compiling foo` and stops, and nothing in what it printed says the kernel
// killed it. The longer the build, the more certain it is to have printed
// something, so the case where this matters most is exactly the one the gate
// removed it from - a substrate build that dies at the link step after five
// hundred lines of progress.
//
// The resource figures stay behind the gate. Those a reader can go and measure;
// the kill they cannot.
func TestAKilledStepSaysSoEvenWhenItPrinted(t *testing.T) {
	t.Parallel()

	chatty := []byte("   Compiling midnight-node v3.0.0\n   Compiling foo v0.1.0\n")

	for what, f := range map[string]failure{
		"out of memory": {exit: -1, oomKills: 1, rss: 8 << 30, ran: time.Minute},
		"signalled":     {exit: -1, signal: syscall.SIGKILL, ran: time.Minute},
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			got := noteFor(chatty, f)
			if got == "" {
				t.Fatalf("a step killed (%s) that had printed said nothing about it", what)
			}

			if f.oomKills > 0 && !strings.Contains(got, "memory") {
				t.Errorf("an OOM kill did not mention memory: %q", got)
			}
		})
	}

	// What a reader can measure for themselves stays behind the gate: a step
	// that printed and merely exited non-zero gets nothing added.
	if got := noteFor(chatty, failure{exit: 1, cpu: time.Second, rss: 1 << 20}); got != "" {
		t.Errorf("an ordinary failure that printed got a note anyway: %q", got)
	}

	// And a silent one still says everything it knows.
	if got := noteFor(nil, failure{exit: 1, ran: time.Second}); got == "" {
		t.Error("a silent failure said nothing")
	}
}

// The kill note is produced by something.
//
// **The failure this guards against has already happened once.** `noteFor` was
// written so that a step which printed is still told the kernel killed it -
// commit cf71e0773, whose message says a process killed for memory "prints
// `Compiling foo` and stops; nothing in its output says the kernel killed it" -
// and the caller was never changed to use it. The helper had the right
// behaviour, its tests passed, and every build kept the old gate, so a chatty
// step that was OOM-killed still reported an exit code and no reason.
//
// It cost two substrate measurements on the day this was written, each
// diagnosed by guessing from a candidate list - which is the thing that commit
// existed to make unnecessary.
//
// A source-level check, and worth being plain about what it proves: that the
// call exists, not that a build reaches it.
func TestTheKillNoteIsProducedBySomething(t *testing.T) {
	t.Parallel()

	callers, err := sourceguard.NonTestFilesContaining(".", "noteFor(")
	if err != nil {
		t.Fatal(err)
	}

	delete(callers, "silentfailure.go")

	if len(callers) == 0 {
		t.Error("nothing outside silentfailure.go calls noteFor" +
			"\n  a step killed for memory then reports its exit code and no reason," +
			"\n  which is the one cause a reader cannot infer from the output")
	}
}
