// Command mutate deletes a mechanism and checks that the suite notices.
//
// The technique that has found every one of this engine's untested invariants:
// take a line the correctness rests on, remove it, and run the tests. A suite
// that stays green is not passing - it is silent about the thing it was written
// to defend.
//
// Five tests in this work asserted an *outcome* and were satisfied by any of its
// causes; each was found this way and none by reading. So the sweep is a tool
// rather than a habit.
//
// # What a result means
//
//	killed     the suite noticed. What is wanted.
//	SURVIVED   nothing noticed. A mechanism with no guard.
//	ANCHOR     the code moved and the catalogue did not. Fix the entry.
//	NOCOMPILE  the mutant is not valid Go. Tests the compiler, not the suite.
//	STUCK      `go test` never finished, so nothing was measured. Not a
//	           survivor: "the tests did not notice" and "the tests never ran"
//	           are different answers.
//	unrun      this platform cannot compile the mechanism at all.
//
// **`unrun` is not `killed`.** A mutation the platform compiled away looks
// exactly like one nothing tested (E241), so it is reported apart and the exit
// status ignores it - a sweep on darwin says nothing whatever about the guest's
// Linux paths, and should say so rather than imply otherwise.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// pending is the file a mutant is applied to right now, and what it said before.
//
// **A sweep that is killed must put the file back.** Three interrupted runs in
// one session left a mutant in the tree - a comparison missing its transfer
// term, a price missing its measurement - each caught by the next test run and
// each committable. The tool that exists to find defects was introducing them
// (E348).
type pending struct {
	path string
	src  []byte
}

var held atomic.Pointer[pending]

// writeSource is os.WriteFile, named so that a test can observe the moment a
// mutant lands on disk. The order of that moment against `holding` is the
// promise this tool makes about never leaving a mutant behind.
var writeSource = os.WriteFile

// holding records what to put back if this process does not finish.
func holding(path string, src []byte) { held.Store(&pending{path: path, src: src}) }

// putBack restores the file a mutant is applied to, once.
//
// **Once**, because the ordinary path restores and clears: a signal arriving
// after that must not overwrite a file somebody has since edited.
func putBack() {
	p := held.Swap(nil)
	if p == nil {
		return
	}

	err := os.WriteFile(p.path, p.src, 0o600)
	if err != nil {
		panic("mutate: could not restore " + p.path + ": " + err.Error())
	}
}

// The verdicts, which are a closed set and are matched as well as printed.
//
// Named because the set grew: STUCK was added when a wedged `go test` turned
// out to read as SURVIVED, and a verdict that is only ever a literal is one a
// new case can be added to without the tally noticing.
const (
	verdictAnchor    = "ANCHOR"
	verdictNoCompile = "NOCOMPILE"
	verdictStuck     = "STUCK"
	verdictSurvived  = "SURVIVED"
	verdictKilled    = "killed"
)

func main() {
	// A sweep is long and gets interrupted - by a timeout, by a person. Putting
	// the file back is the last thing this process does either way.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		putBack()
		os.Exit(1)
	}()

	root := flag.String("C", ".", "repository root")
	only := flag.String("run", "", "only mutants whose name contains this")
	timeout := flag.Duration("timeout", 5*time.Minute, "per-mutant test timeout")
	// **Compile each mutant instead of testing it.** A mutant that is not valid
	// Go tests the compiler rather than the suite, and until now the only way
	// to find one was a full sweep: hours, to learn that an entry had been
	// wrong since somebody edited the code near it. Building is seconds a
	// mutant, so the catalogue's validity is checkable on its own.
	compileOnly := flag.Bool("compile", false, "only check each mutant still compiles")
	flag.Parse()

	// Before the first mutant, because the damage a second sweep does is to the
	// files this one is about to read.
	unlock, err := lockSweep(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mutate:", err)
		os.Exit(1)
	}

	defer unlock()

	survived, problems := 0, 0

	for _, m := range Mutants {
		if *only != "" && !strings.Contains(m.Name, *only) {
			continue
		}

		if m.OS != "" && runtime.GOOS != m.OS {
			fmt.Printf("%-10s %s\n", "unrun", m.Name)

			continue
		}

		verdict, detail := run(*root, m, *timeout, *compileOnly)

		fmt.Printf("%-10s %s\n", verdict, m.Name)

		if detail != "" {
			fmt.Printf("%-10s   %s\n", "", detail)
		}

		switch verdict {
		case verdictSurvived:
			survived++
		case verdictAnchor, verdictNoCompile, verdictStuck:
			// STUCK counts as a problem rather than as a survivor: nothing was
			// measured, and a mutant nobody measured must not read as one
			// nobody caught.
			problems++
		}
	}

	if m := note(survived, problems); m != "" {
		fmt.Fprintln(os.Stderr, m)

		// Explicitly, because `os.Exit` does not run deferred functions. The
		// kernel would drop the lock anyway when this process ends - that is
		// why it is an flock - but a reader should not have to know that to
		// see that the lock is released.
		unlock()
		//nolint:gocritic // exitAfterDefer: unlock() is called explicitly above
		os.Exit(1)
	}
}

// note is what to say at the end, or nothing.
func note(survived, problems int) string {
	switch {
	case survived > 0 && problems > 0:
		return fmt.Sprintf("%d mechanism(s) nothing guards, and %d catalogue"+
			" entries that no longer apply", survived, problems)

	case survived > 0:
		return fmt.Sprintf("%d mechanism(s) can be deleted without any test"+
			" noticing", survived)

	case problems > 0:
		return fmt.Sprintf("%d catalogue entries no longer apply; the code"+
			" moved and the anchors did not", problems)

	default:
		return ""
	}
}

// run applies one mutant, tests, and puts the file back.
func run(root string, m Mutant, timeout time.Duration, compileOnly bool) (verdict, detail string) {
	path := filepath.Join(root, m.File)

	src, err := os.ReadFile(path) //nolint:gosec // a path from the catalogue
	if err != nil {
		return verdictAnchor, err.Error()
	}

	if n := strings.Count(string(src), m.Anchor); n != 1 {
		return verdictAnchor, fmt.Sprintf("%d matches in %s, want exactly 1", n, m.File)
	}

	mutant := strings.Replace(string(src), m.Anchor, m.Replacement, 1)

	// The path comes from the catalogue, which is a literal in this repository
	// and not anybody's input (gosec G703).
	// **Registered before the write, not after.** `os.WriteFile` truncates
	// before it writes, so a write that fails part of the way through leaves
	// the file damaged - and registering afterwards means that failure returns
	// with a mutant on disk and nothing that knows how to put it back. The
	// signal handler is blind for the same window.
	holding(path, src)

	// **Restored whatever happens**, including a panic in this process: a sweep
	// that left a mutant behind would be a defect committed by the tool written
	// to find defects.
	//
	// A `defer` is not "whatever happens" - it does not run when the process is
	// killed, which is exactly how an interrupted sweep ends. `holding` above
	// and the signal handler in `main` cover that; this covers the rest.
	defer putBack()

	err = writeSource(path, []byte(mutant), 0o600)
	if err != nil {
		return verdictAnchor, err.Error()
	}

	// **Bounded twice, because the two bounds catch different failures.**
	// `-timeout` is go test's own and stops a *test* that hangs, reporting
	// which one. It does nothing about `go test` itself wedging - waiting on a
	// module download, a build cache lock, a child that ignored its parent -
	// and a sweep of four hundred mutants that stops making progress looks
	// exactly like one that is merely slow.
	//
	// The outer bound is deliberately the looser of the two, so the inner one
	// reports first wherever it can: a named test is a better answer than a
	// killed process.
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Minute)
	defer cancel()

	// G204: the package comes from this tool's own catalogue, which is a Go
	// file in this repository and not anybody's input.
	args := []string{"test", m.Package, "-count=1", "-timeout", timeout.String()}
	if compileOnly {
		// `vet` rather than `build`, because a mutant lands in a test file as
		// often as in a source one and `go build` does not compile tests.
		args = []string{"vet", m.Package}
	}

	//nolint:gosec // G204: the package comes from the catalogue above
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	text := string(out)

	// In compile-only mode the question is just whether it is valid Go: a
	// mutant that builds has nothing more to say here, and one that does not is
	// a catalogue entry to fix.
	if compileOnly {
		if err != nil {
			// Any error line, not only the two shapes the test path looks for.
			// A mutant can fail to compile in ways nobody predicted, and
			// reporting nothing for those made half of them look detail-free
			// when the compiler had said exactly what was wrong.
			return verdictNoCompile, firstLine(text, ".go:")
		}

		return verdictKilled, ""
	}

	switch {
	case ctx.Err() != nil:
		// The outer bound fired, so `go test` itself was stuck rather than a
		// test in it. Reported as its own verdict: "the tests did not notice"
		// and "the tests never ran" are different answers about a mutant, and
		// counting the second as the first records a mechanism as unguarded
		// when nothing has been measured at all.
		return verdictStuck, "go test did not finish within " + (timeout + time.Minute).String()

	case strings.Contains(text, "[build failed]"),
		strings.Contains(text, "declared and not used"):
		return verdictNoCompile, firstLine(text, "declared and not used", "undefined:")

	case err == nil:
		return verdictSurvived, ""

	default:
		return verdictKilled, firstLine(text, "--- FAIL")
	}
}

// firstLine is the first line mentioning any of these, for a one-line report.
func firstLine(text string, marks ...string) string {
	for line := range strings.SplitSeq(text, "\n") {
		for _, m := range marks {
			if strings.Contains(line, m) {
				return strings.TrimSpace(line)
			}
		}
	}

	return ""
}
