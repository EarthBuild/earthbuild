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
//	unrun      this platform cannot compile the mechanism at all.
//
// **`unrun` is not `killed`.** A mutation the platform compiled away looks
// exactly like one nothing tested (E241), so it is reported apart and the exit
// status ignores it - a sweep on darwin says nothing whatever about the guest's
// Linux paths, and should say so rather than imply otherwise.
package main

import (
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

	if err := os.WriteFile(p.path, p.src, 0o600); err != nil {
		panic("mutate: could not restore " + p.path + ": " + err.Error())
	}
}

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
	flag.Parse()

	survived, problems := 0, 0

	for _, m := range Mutants {
		if *only != "" && !strings.Contains(m.Name, *only) {
			continue
		}

		if m.Linux && runtime.GOOS != "linux" {
			fmt.Printf("%-10s %s\n", "unrun", m.Name)

			continue
		}

		verdict, detail := run(*root, m, *timeout)

		fmt.Printf("%-10s %s\n", verdict, m.Name)

		if detail != "" {
			fmt.Printf("%-10s   %s\n", "", detail)
		}

		switch verdict {
		case "SURVIVED":
			survived++
		case "ANCHOR", "NOCOMPILE":
			problems++
		}
	}

	if m := note(survived, problems); m != "" {
		fmt.Fprintln(os.Stderr, m)
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
func run(root string, m Mutant, timeout time.Duration) (verdict, detail string) {
	path := filepath.Join(root, m.File)

	src, err := os.ReadFile(path) //nolint:gosec // a path from the catalogue
	if err != nil {
		return "ANCHOR", err.Error()
	}

	if n := strings.Count(string(src), m.Anchor); n != 1 {
		return "ANCHOR", fmt.Sprintf("%d matches in %s, want exactly 1", n, m.File)
	}

	mutant := strings.Replace(string(src), m.Anchor, m.Replacement, 1)

	err = os.WriteFile(path, []byte(mutant), 0o600)
	if err != nil {
		return "ANCHOR", err.Error()
	}

	holding(path, src)

	// **Restored whatever happens**, including a panic in this process: a sweep
	// that left a mutant behind would be a defect committed by the tool written
	// to find defects.
	//
	// A `defer` is not "whatever happens" - it does not run when the process is
	// killed, which is exactly how an interrupted sweep ends. `holding` above
	// and the signal handler in `main` cover that; this covers the rest.
	defer putBack()

	cmd := exec.Command("go", "test", m.Package, "-count=1",
		"-timeout", timeout.String())
	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	text := string(out)

	switch {
	case strings.Contains(text, "[build failed]"),
		strings.Contains(text, "declared and not used"):
		return "NOCOMPILE", firstLine(text, "declared and not used", "undefined:")

	case err == nil:
		return "SURVIVED", ""

	default:
		return "killed", firstLine(text, "--- FAIL")
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
