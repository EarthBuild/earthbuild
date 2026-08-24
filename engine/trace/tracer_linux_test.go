//go:build linux

package trace

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The sightings are sorted, and not by chance.
//
// Three paths come out of a map in sorted order about one time in six, which is
// how a version of this that stopped sorting stayed green. Enough entries here
// that the arrangement cannot happen: 40! is not a number anything is one in.
//
// Sorted matters because an observation's order reaches the key derived from it,
// so a map's iteration order would make a step's identity depend on scheduling
// (I12).
func TestTheSightingsAreSortedNotMerelyOftenSorted(t *testing.T) {
	t.Parallel()

	tr := NewTracer(-1)

	// Inserted in an order that is already wrong, so a tracer that returned
	// insertion order would fail too.
	for i := 40; i > 0; i-- {
		tr.paths["/p/"+strconv.Itoa(i)] = true
	}

	got := tr.Sightings()

	if !slices.IsSorted(got.Paths) {
		t.Errorf("40 paths came back unsorted, so nothing sorts them: %v",
			got.Paths[:5])
	}
}

// The reasons are sorted too, and there are only three of them.
//
// Which is the difficulty: three items come out of a map in order one time in
// six, so a single check is a coin that lands the wrong way often enough to have
// let exactly this mutation through. There is no scaling the set - the reasons
// are a closed list - so the trial is repeated instead, on a fresh tracer each
// time. Twenty-five of them agreeing by chance is one in six to the
// twenty-fifth.
func TestTheReasonsAreSortedEveryTimeAndNotOftenEnough(t *testing.T) {
	t.Parallel()

	all := []string{whyForeignArch, whyUnknownCall, whyUnreadable}

	want := slices.Clone(all)
	slices.Sort(want)

	for i := range 25 {
		tr := NewTracer(-1)

		// Inserted in a different rotation each round, so an implementation
		// returning insertion order fails on the first one.
		for j := range all {
			tr.lose(all[(i+j)%len(all)])
		}

		if got := tr.Sightings().Why; !slices.Equal(got, want) {
			t.Fatalf("round %d: reasons came back as %v, want %v", i, got, want)
		}
	}
}

// A step's sightings are what it looked at, sorted and each once.
//
// Sorted because two runs that see the same paths in a different order must
// produce the same value - a map's iteration order would make the observation,
// and so the key derived from it, depend on scheduling (I12).
func TestSightingsAreSortedAndDeduplicated(t *testing.T) {
	dir := t.TempDir()

	names := []string{"c-5f1.txt", "a-5f1.txt", "b-5f1.txt"}
	for _, n := range names {
		err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	tr := withTracer(t, func() {
		// Out of order, and each twice.
		for range 2 {
			for _, n := range names {
				fd, err := unix.Openat(unix.AT_FDCWD, filepath.Join(dir, n),
					unix.O_RDONLY, 0)
				if err == nil {
					_ = unix.Close(fd)
				}
			}
		}
	})

	got := tr.Sightings()

	if !slices.IsSorted(got.Paths) {
		t.Errorf("the paths are not sorted: %v", got.Paths)
	}

	for i := 1; i < len(got.Paths); i++ {
		if got.Paths[i] == got.Paths[i-1] {
			t.Errorf("%q appears twice", got.Paths[i])
		}
	}

	for _, n := range names {
		want := filepath.Join(dir, n)
		if !slices.Contains(got.Paths, want) {
			// **What was seen, not only what was missed.** This failed on a
			// hosted runner and said which path was absent, which is the one
			// thing that cannot explain why: an empty list and a list of
			// different paths fail identically here, and they have nothing in
			// common as causes (E609).
			t.Errorf("%q was opened twice and is not among the %d sightings: %v",
				want, len(got.Paths), got.Paths)
		}
	}
}

// A call in another architecture's numbering is declared, not decoded.
//
// The numbers **overlap**: i386's 5 is `open` and x86-64's 5 is `fstat`. So a
// foreign call reaches a real entry in the table and reads whichever argument
// that entry names - a confident, wrong path, recorded as a read the step never
// made. Checking the architecture before the number is what prevents it, and
// declaring the gap is what keeps the step cacheable without being reusable
// against the wrong base (I3).
func TestAForeignArchitectureIsDeclaredRatherThanDecoded(t *testing.T) {
	t.Parallel()

	tr := NewTracer(-1)

	n := seccompNotif{Pid: uint32(os.Getpid())}
	n.Data.Arch = auditArch ^ 1
	n.Data.NR = int32(traced[0])
	// An address that would read perfectly well if anybody looked at it.
	n.Data.Args[1] = 0

	tr.handle(n)

	got := tr.Sightings()

	if !got.Incomplete {
		t.Error("a call this engine cannot interpret left the observation" +
			" complete; the reads inside it are exactly the ones that would" +
			" make an L2 hit wrong")
	}

	if len(got.Paths) != 0 {
		t.Errorf("a foreign call contributed %v; its argument numbering is not"+
			" ours and whatever was read is not a path the step named",
			got.Paths)
	}

	// The reason, not merely the fact. Without the architecture check this
	// notification is still lost - the table is consulted, the argument it names
	// is not an address, and the read fails - so "incomplete" alone cannot tell
	// a checked architecture from an unchecked one. It survived that mutation
	// until this assertion existed (E209).
	if !because(got.Why, whyForeignArch) {
		t.Errorf("the observation was lost for %v, not for the architecture;"+
			" the check that would have caught this before the syscall number"+
			" was believed is not running", got.Why)
	}
}

// A path that cannot be read declares the gap rather than losing one entry.
//
// Unreadable is not absent. The step named something, and recording one fewer
// path would be a claim this engine cannot make - a base missing that file would
// then satisfy the observation.
func TestAnUnreadablePathDeclaresTheObservationIncomplete(t *testing.T) {
	t.Parallel()

	tr := NewTracer(-1)

	n := seccompNotif{Pid: uint32(os.Getpid())}
	n.Data.Arch = auditArch
	n.Data.NR = int32(traced[0])
	// Never mapped, so the read fails outright.
	n.Data.Args[1] = 0

	tr.handle(n)

	got := tr.Sightings()

	if !got.Incomplete {
		t.Error("a path that could not be read left the observation complete")
	}

	// A prefix, because the reason now carries the errno: `whyUnreadable` names
	// the class and the system error says which one, which is the difference
	// between "this step will never earn an L2 hit" and knowing why (E215).
	if !because(got.Why, whyUnreadable) {
		t.Errorf("the observation was lost for %v, not for the path", got.Why)
	}

	if len(got.Paths) != 0 {
		t.Errorf("an unreadable path contributed %v", got.Paths)
	}
}

// Nothing seen means nothing missed.
//
// The default must be complete rather than incomplete: a step that opened
// nothing has been fully observed, and starting from "incomplete" would make
// every trivial step permanently unreusable while looking like caution.
func TestAStepThatLookedAtNothingIsCompletelyObserved(t *testing.T) {
	t.Parallel()

	got := NewTracer(-1).Sightings()

	if got.Incomplete {
		t.Error("a tracer that saw nothing reports an incomplete observation")
	}

	if len(got.Paths) != 0 {
		t.Errorf("a tracer that saw nothing reports %v", got.Paths)
	}
}

// withTracer runs body on a filtered thread with a real Tracer answering for it.
func withTracer(t *testing.T, body func()) *Tracer {
	t.Helper()

	ready := make(chan int, 1)
	failed := make(chan error, 1)
	finished := make(chan struct{})

	park := parking(t)

	go func() {
		// Never unlocked: the filter cannot be removed, so the thread has to be
		// destroyed rather than returned to the pool (E206).
		runtime.LockOSThread()

		fd, err := install(auditArch, traced)
		if err != nil {
			failed <- err

			return
		}

		ready <- fd
		body()
		close(finished)

		park()
	}()

	var fd int

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case fd = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the filter was never installed")
	}

	tr := NewTracer(fd)
	go tr.Run()

	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("the filtered work never finished; a notification went unanswered")
	}

	return tr
}

// because reports whether an observation was lost for a given reason.
//
// A prefix rather than equality: a reason may carry detail after it - an errno,
// for the unreadable case - and a test asserting the class should not have to
// know which system error a particular machine produced.
func because(why []string, reason string) bool {
	for _, w := range why {
		if w == reason || strings.HasPrefix(w, reason+":") {
			return true
		}
	}

	return false
}

// A file the step writes is not a file it read.
//
// `cat x > out` opens `out` write-only, and to the tracer that is a path being
// named like any other. Recorded as a read it becomes a prediction naming the
// step's **own output** - which no base can contain, so it is stale on every
// later build and the step is never reused:
//
//	1 of 2 predictions stale (/w/out.txt is gone from the base)
//
// That is not a false hit, so nothing is unsafe about it; it is the tier
// silently never working, which is the failure this whole line of work keeps
// producing in new disguises (E217).
//
// Read-write is deliberately *not* skipped. It may read, and recording a read
// that did not happen costs a miss while missing one that did costs a false hit.
func TestAWriteOnlyOpenIsNotARead(t *testing.T) {
	t.Parallel()

	// A variable, because Go folds the conversion of a negative constant to an
	// unsigned type and refuses it - the kernel has no such scruples (E208).
	fdcwd := int32(unix.AT_FDCWD)

	for _, tc := range []struct {
		name  string
		flags uint64
		read  bool
	}{
		{"write only", unix.O_WRONLY, false},
		{"truncating write", unix.O_WRONLY | unix.O_CREAT | unix.O_TRUNC, false},
		{"read only", unix.O_RDONLY, true},
		{"read write", unix.O_RDWR, true},
		{"read write creating", unix.O_RDWR | unix.O_CREAT, true},
	} {
		tr := NewTracer(-1)

		n := seccompNotif{Pid: uint32(os.Getpid())} //nolint:gosec // a pid is not negative
		n.Data.Arch = auditArch
		n.Data.NR = unix.SYS_OPENAT
		n.Data.Args[0] = uint64(uint32(fdcwd)) //nolint:gosec // as the kernel delivers it
		n.Data.Args[1] = 0                     // an address that cannot be read
		n.Data.Args[2] = tc.flags

		tr.handle(n)

		// The path is unreadable either way, so the reads are distinguished by
		// whether the tracer *tried*: a skipped write does not declare a gap,
		// while an attempted read that failed does.
		got := tr.Sightings()

		if tc.read && !got.Incomplete {
			t.Errorf("%s: the tracer did not attempt the path, so an open that"+
				" may read was treated as a write", tc.name)
		}

		if !tc.read && got.Incomplete {
			t.Errorf("%s: the tracer attempted the path of a write-only open;"+
				" the step's own output would be recorded as an input", tc.name)
		}
	}
}

// The root is not a read.
//
// A step that stats `/` has learned nothing that should key it - "the filesystem
// has a root" decides no behaviour - while the root's digest carries a mode, an
// owner and a timestamp that move whenever *anything* is layered on the base.
// Recording it makes a step stale on every base change there is, which is the
// opposite of what the tier is for.
//
// Measured on the corpus: `1 of 2 predictions stale (/ changed in the base)` on
// three targets, against a perturbation that added an empty layer and touched
// nothing else (E221).
//
// The copy path reached this conclusion first and its reasoning is the same one:
// `observeDest` walks a destination's ancestors and stops *above* the root,
// because the root's digest differs between two bases for reasons no step
// depends on.
func TestTheRootIsNotARead(t *testing.T) {
	t.Parallel()

	tr := NewTracer(-1)

	tr.record("/")
	tr.record("/etc/passwd")

	got := tr.Sightings()

	if slices.Contains(got.Paths, "/") {
		t.Error("the root was recorded as a read; every base change moves its" +
			" digest, so the step is stale whatever it actually looked at")
	}

	if !slices.Contains(got.Paths, "/etc/passwd") {
		t.Errorf("an ordinary path was dropped along with the root: %v", got.Paths)
	}

	if got.Incomplete {
		t.Error("dropping the root declared the observation incomplete;" +
			" it is not a gap, it is a path that says nothing")
	}
}
