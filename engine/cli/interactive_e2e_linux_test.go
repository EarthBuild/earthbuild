package cli_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/creack/pty"
)

// A person types, and the step reads it.
//
// The whole chain, end to end: the CLI finds its terminal (E196), the
// interpreter accepts the construct because there is one (E195), the executor
// hands the descriptor to the guest (E193), and the step owns it (E190). Any
// link broken and this fails.
//
// Input, not just output. A step that can print to a terminal has half of one;
// `read` is what an interactive session is for, and it is the half that a relay
// through a byte stream would get wrong.
//
// In a child with a pty as its controlling terminal, because `go test` has none
// and the CLI - correctly - refuses the construct without one.
func TestABuildPromptsAndReadsTheAnswer(t *testing.T) {
	if os.Getenv("EARTH_TEST_INTERACTIVE_CHILD") != "" {
		interactiveChild()

		return
	}

	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	// Built before asking whether a sandbox is available, because that question
	// is answered by looking for this binary.
	guestd := buildGuestd(t)
	t.Setenv("EARTH_GUESTD", guestd)

	// not parallel: boots a sandbox
	requireSandbox(t)

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}

	t.Cleanup(func() { _ = ptmx.Close() })

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot find this test binary: %v", err)
	}

	cmd := exec.Command(self, "-test.run", "^TestABuildPromptsAndReadsTheAnswer$") //nolint:gosec // this binary
	cmd.Env = append(os.Environ(),
		"EARTH_TEST_INTERACTIVE_CHILD=1",
		"EARTH_GUESTD="+guestd,
		"EARTH_IMAGE_CACHE_DIR="+sharedImages(t),
		"EARTH_TEST_STORE="+t.TempDir(),
	)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}

	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	_ = tty.Close()

	lines := make(chan string, 32)

	go func() {
		sc := bufio.NewScanner(ptmx)
		for sc.Scan() {
			lines <- strings.TrimSpace(sc.Text())
		}

		close(lines)
	}()

	answered := false
	deadline := time.After(10 * time.Minute)

	// Everything the terminal said, for the failure message. A session that ends
	// early is a session whose last words are the whole diagnosis, and throwing
	// them away leaves "it did not work".
	var seen []string

	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("the terminal closed before the step answered; it said:\n  %s",
					strings.Join(seen, "\n  "))
			}

			seen = append(seen, l)

			// The prompt. Typed into, exactly as a person would.
			if !answered && strings.Contains(l, "WHO-GOES-THERE") {
				answered = true

				_, _ = ptmx.Write([]byte("friend\n"))
			}

			if strings.Contains(l, "GOT-friend") {
				if err := cmd.Wait(); err != nil {
					t.Errorf("the step read the answer and the build still failed: %v", err)
				}

				return
			}

		case <-deadline:
			_ = cmd.Process.Kill()

			t.Fatal("the step never read what was typed at it")
		}
	}
}

// interactiveChild runs the build, from a process that has a terminal.
func interactiveChild() {
	dir, err := os.MkdirTemp("", "interactive-*") //nolint:usetesting // no *testing.T here
	if err != nil {
		fmt.Fprintln(os.Stderr, "child:", err)
		os.Exit(3)
	}

	err = os.WriteFile(dir+"/Earthfile", []byte(`VERSION 0.8

main:
    FROM alpine:3.22
    RUN --interactive /bin/busybox sh -c 'echo WHO-GOES-THERE; read x; echo GOT-$x'
`), 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "child:", err)
		os.Exit(4)
	}

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "main", Out: os.Stdout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "child: the build failed:", err)
		os.Exit(5)
	}
}
