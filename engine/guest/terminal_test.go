package guest_test

import (
	"bufio"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/creack/pty"
)

// A step attached to a terminal sees one.
//
// E189 established that a descriptor can be handed to the guest and that this is
// what a terminal has to be. This is the other half: a step started with that
// descriptor must actually *have* a controlling terminal, not merely have its
// streams pointed at one.
//
// The distinction is `setsid` and `TIOCSCTTY`. Without them a shell's `test -t 0`
// is true and everything else about a terminal is false: no job control, no
// signal on Ctrl-C, and a `read` that cannot be interrupted. That is the shape
// of an interactive session that looks right until somebody needs it.
//
// pty comes from `github.com/creack/pty`, already a direct dependency of this
// repository - `cmd/debugger` uses it - so the interactive construct costs no
// new supply-chain surface.
func TestAStepAttachedToATerminalHasOne(t *testing.T) {
	t.Parallel()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty on this machine: %v", err)
	}

	t.Cleanup(func() { _ = ptmx.Close() })

	// `test -t 0` says the descriptor is a terminal. Opening `/dev/tty` says
	// whether this process has a *controlling* one - that is what the path
	// means, and it is the definition rather than a proxy for it. `ps -o stat=`
	// would do as well on a developer machine and not in a busybox container,
	// where the flag is not supported.
	cmd := exec.CommandContext(t.Context(), testShell, "-c",
		// The redirection is inside a subshell so that its *failure* message is
		// caught too: `2>/dev/null` on the compound covers the command's stderr
		// and not the shell's complaint about the redirect, which then arrives
		// as an extra line and is read as the answer.
		`test -t 0 && echo IS-TTY; if (: < /dev/tty) 2>/dev/null; then echo HAS-CTTY; else echo NO-CTTY; fi`)

	err = guest.AttachTerminal(cmd, tty)
	if err != nil {
		t.Fatal(err)
	}

	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	// The guest keeps no copy: the child owns it now, and holding one here would
	// stop the reader below ever seeing EOF.
	_ = tty.Close()

	t.Cleanup(func() { _ = cmd.Wait() })

	lines := make(chan string, 4)

	go func() {
		sc := bufio.NewScanner(ptmx)
		for sc.Scan() {
			lines <- strings.TrimSpace(sc.Text())
		}

		close(lines)
	}()

	var saw []string

	deadline := time.After(10 * time.Second)

	for len(saw) < 2 {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("the terminal closed after %v", saw)
			}

			if l != "" {
				saw = append(saw, l)
			}
		case <-deadline:
			t.Fatalf("the step said %v and then nothing", saw)
		}
	}

	if saw[0] != "IS-TTY" {
		t.Errorf("the step's stdin is not a terminal: %q", saw)
	}

	// **Not** a controlling terminal, and that is the decision rather than a
	// shortfall.
	//
	// A terminal can be the controlling terminal of one session, and the
	// caller's terminal is already the caller's - measured in E197, where a
	// second claim answers `operation not permitted`. So the step gets the
	// terminal on its streams and the session stays with the engine, which is
	// what makes Ctrl-C cancel the build rather than one step of it (E179).
	//
	// Pinned here so that a later change to a relayed inner pty - which would
	// give the step a controlling terminal of its own - has to come and edit
	// this sentence rather than quietly satisfy it.
	if saw[1] != "NO-CTTY" {
		t.Errorf("the step has a controlling terminal (%q); it was given the"+
			" caller's, which the caller still owns", saw[1])
	}
}
