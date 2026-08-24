package guest_test

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A step's fault-in reaches the machine that can answer it.
//
// The tracer runs **inside** the guest, and the peers live outside it: the guest
// is confined on purpose, and the fetcher holds addresses, connections and a
// store it must not. So a fault-in is a request in the other direction from
// every other message in this protocol - the guest asks, the host answers
// (E291).
func TestAFaultInReachesTheMachineThatCanAnswerIt(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	var (
		mu    sync.Mutex
		asked []string
	)

	go func() {
		_ = guest.ServeFills(there, func(_, path string) error {
			mu.Lock()
			asked = append(asked, path)
			mu.Unlock()

			return nil
		})
	}()

	f := guest.NewFills(here)

	err := f.Fill("/base/usr/bin/cc")
	if err != nil {
		t.Fatalf("%v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(asked) != 1 || asked[0] != "/base/usr/bin/cc" {
		t.Errorf("the host was asked %v", asked)
	}
}

// A host that could not obtain the file says so, and the guest passes it on.
//
// The distinction the whole of E289 turns on, carried across a wire: the host
// answers "absent" by succeeding and "unreachable" by failing, and the guest
// must not flatten the two.
func TestAHostThatCouldNotObtainAFileSaysSo(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	go func() {
		_ = guest.ServeFills(there, func(string, string) error {
			return errors.New("the peer went away")
		})
	}()

	err := guest.NewFills(here).Fill("/base/usr/bin/cc")
	if err == nil {
		t.Fatal("a host that could not obtain the file reported success")
	}

	if !strings.Contains(err.Error(), "went away") {
		t.Errorf("%v; the reason has to survive the wire, or nobody can tell a"+
			" peer that vanished from a file that was never there", err)
	}
}

// A host that has gone away is a failure, never a silent absence.
//
// **The failure mode this protocol exists to prevent.** If the channel breaks
// and the guest treats that as "no such file", the step takes the other branch
// and produces a layer keyed on a lie - and nothing anywhere reports a problem.
func TestAHostThatHasGoneAwayFailsRatherThanAnswersNothing(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	// The host hangs up without answering.
	_ = there.Close()

	err := guest.NewFills(here).Fill("/base/usr/bin/cc")
	if err == nil {
		t.Fatal("a broken channel was read as a file that is not there")
	}
}

// Answers find the request that asked for them.
//
// A step opens files from several threads at once, and the answers come back in
// whatever order the host produced them. Matching by arrival would hand one
// fault-in another's outcome - which, when one succeeded and one did not, is the
// lie again.
func TestAnswersFindTheRequestThatAskedForThem(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	go func() {
		_ = guest.ServeFills(there, func(_, path string) error {
			// The slow one is the one that fails, so an answer matched by
			// arrival would give the wrong verdict to both.
			if strings.HasSuffix(path, "slow") {
				time.Sleep(50 * time.Millisecond)

				return errors.New("no")
			}

			return nil
		})
	}()

	f := guest.NewFills(here)

	var (
		wg               sync.WaitGroup
		slowErr, fastErr error
	)

	wg.Add(2)

	go func() { defer wg.Done(); slowErr = f.Fill("/base/slow") }()

	time.Sleep(10 * time.Millisecond)

	go func() { defer wg.Done(); fastErr = f.Fill("/base/fast") }()

	wg.Wait()

	if slowErr == nil {
		t.Error("the slow fault-in was told the fast one's answer")
	}

	if fastErr != nil {
		t.Errorf("the fast fault-in was told the slow one's answer: %v", fastErr)
	}
}

// The guest remembers what it faulted in, and what it was.
//
// The capture has to leave those files out (E293), and **the guest is the only
// party that knows all of them**: it asked for each one. The digest comes with
// the path because the exclusion is by name and by content both - a file the
// step then edits is the step's after all (E293), and telling the two apart
// needs to know what was placed.
func TestTheGuestRemembersWhatItFaultedIn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	at := filepath.Join(dir, "libc.so")
	body := []byte("from the base\n")

	here, there := net.Pipe()

	go func() {
		_ = guest.ServeFills(there, func(_, p string) error {
			if p != at {
				return nil // absent, and not an error
			}

			return os.WriteFile(p, body, 0o600)
		})
	}()

	f := guest.NewFills(here)

	err := f.Fill(at)
	if err != nil {
		t.Fatal(err)
	}

	// And one the host looked for and did not find.
	err = f.Fill(filepath.Join(dir, "not-in-the-base"))
	if err != nil {
		t.Fatal(err)
	}

	got := f.FilledFor("")

	if len(got) != 1 {
		t.Fatalf("remembered %v; only what actually arrived belongs there"+
			"\n  a path the base does not have was never placed, and excluding"+
			" it would exclude nothing", got)
	}

	if got[at] != layer.ContentID(body) {
		t.Errorf("remembered %s as %v, want %v", at, got[at], layer.ContentID(body))
	}
}

// A fill that failed is not remembered as placed.
//
// It is remembered as fatal instead - the step is failed - and recording it here
// as well would exclude a file that is not there from a capture that will never
// happen.
func TestAFailedFillIsNotRememberedAsPlaced(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	go func() {
		_ = guest.ServeFills(there, func(string, string) error {
			return errors.New("the peer went away")
		})
	}()

	f := guest.NewFills(here)

	_ = f.Fill(filepath.Join(t.TempDir(), "never-arrives"))

	if got := f.FilledFor(""); len(got) != 0 {
		t.Errorf("remembered %v after a fetch that failed", got)
	}
}

// A fault-in says which base it is for.
//
// **Found by trying to answer one.** A worker runs several steps at once, each
// with its own base; a request that named only a path would leave the host
// guessing which stack to fetch from - and guessing wrong serves one step a file
// out of another step's base, which is a wrong build that reports success
// (E303).
//
// The guest knows: it is holding the handle the step is running against. So it
// says.
func TestAFaultInSaysWhichBaseItIsFor(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	type asked struct{ handle, path string }

	var (
		mu   sync.Mutex
		seen []asked
	)

	go func() {
		_ = guest.ServeFills(there, func(handle, path string) error {
			mu.Lock()
			seen = append(seen, asked{handle, path})
			mu.Unlock()

			return nil
		})
	}()

	f := guest.NewFills(here)

	err := f.For("h1", "/base-one")("/base-one/usr/bin/cc")
	if err != nil {
		t.Fatal(err)
	}

	err = f.For("h2", "/base-two")("/base-two/usr/bin/cc")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(seen) != 2 {
		t.Fatalf("the host was asked %v", seen)
	}

	if seen[0].handle == seen[1].handle {
		t.Errorf("two steps' fault-ins arrived under one handle: %v"+
			"\n  the host would fetch both from one base", seen)
	}
}

// What a handle faulted in is remembered against that handle.
//
// The capture excludes what was faulted into *its* delta (E295), and two steps
// running at once each have one. A single list would have each step excluding
// the other's files - so one layer loses writes it made and the other keeps a
// base file it never wrote.
func TestFaultInsAreRememberedAgainstTheirHandle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	here, there := net.Pipe()

	go func() {
		_ = guest.ServeFills(there, func(_, p string) error {
			return os.WriteFile(p, []byte("x"), 0o600)
		})
	}()

	f := guest.NewFills(here)

	one := filepath.Join(dir, "one")
	two := filepath.Join(dir, "two")

	err := f.For("h1", dir)(one)
	if err != nil {
		t.Fatal(err)
	}

	err = f.For("h2", dir)(two)
	if err != nil {
		t.Fatal(err)
	}

	if got := f.FilledFor("h1"); len(got) != 1 {
		t.Errorf("handle h1 remembers %v, want just its own", got)
	}

	if _, ok := f.FilledFor("h1")[two]; ok {
		t.Error("h1 remembers a file h2 faulted in" +
			"\n  its capture would exclude a file it never placed")
	}
}

// The directories above a faulted-in file are base, up to the step's root.
//
// **Sound, and bounded.** A directory between the root and a faulted-in path is
// either one priming created or one the fault-in created - both are base, and
// neither belongs in the step's delta (E306, E307).
//
// It cannot be one the *step* created: if the step made it, the base did not
// have it, and a fault-in for a path underneath would have found nothing to
// fetch. So every ancestor up to the root is safe to exclude, and the root is
// what stops the walk reaching `/var` and `/tmp` - which is what an unbounded
// version did.
func TestTheDirectoriesAboveAFaultedInFileAreBase(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	deep := filepath.Join(root, "usr", "lib", "libc.so")

	here, there := net.Pipe()

	go func() {
		_ = guest.ServeFills(there, func(_, p string) error {
			err := os.MkdirAll(filepath.Dir(p), 0o750)
			if err != nil {
				return err
			}

			return os.WriteFile(p, []byte("from the base\n"), 0o600)
		})
	}()

	f := guest.NewFills(here)

	err := f.For("h1", root)(deep)
	if err != nil {
		t.Fatal(err)
	}

	got := f.FilledFor("h1")

	for _, want := range []string{deep, filepath.Join(root, "usr", "lib"), filepath.Join(root, "usr")} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s is not recorded as base; it would land in the step's"+
				" layer", want)
		}
	}

	// And nothing above the root, which is somebody else's filesystem.
	for p := range got {
		if len(p) < len(root) {
			t.Errorf("%s is above the step's root and was recorded as base", p)
		}
	}

	if _, ok := got[root]; ok {
		t.Error("the root itself was recorded; a step's own root is not" +
			" something the engine placed inside it")
	}
}

// A host that vanishes mid-request is a failure, never a silent absence.
//
// **The step would otherwise be keyed on a lie.** A fault-in has two honest
// answers - the file arrived, or the host looked and it is not there - and the
// second is a fact the step is entitled to act on: it takes the other branch,
// and the layer it produces is correct for a base without that file.
//
// A host that went away is neither. Reporting it as "no such file" makes the
// step produce that same layer, cached under a key that says the file was
// absent, with nothing anywhere reporting a problem. Every later build that
// hits the key inherits it (I3, E291).
//
// The wire is closed *after* the request has been read, which is the branch this
// is about. Closing it earlier fails the encode instead, which is a different
// error on a different line and would leave this one untested - as it was.
func TestAHostThatVanishesIsNotAnAbsentFile(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	go func() {
		// One whole request, then nothing: the host has heard the question and
		// died before answering it.
		sc := bufio.NewScanner(there)
		sc.Scan()

		_ = there.Close()
	}()

	err := guest.NewFills(here).Fill("/base/usr/bin/cc")
	if err == nil {
		t.Fatal("a host that went away was reported as 'no such file'," +
			" so the step would take the absent branch and cache a layer" +
			" keyed on a file that was never looked for (I3, E291)")
	}

	// Named, because "the fault-in failed" is not actionable and "the host went
	// away while we were asking about this path" is.
	if !strings.Contains(err.Error(), "/base/usr/bin/cc") {
		t.Errorf("the failure does not say what was being asked for: %v", err)
	}
}
