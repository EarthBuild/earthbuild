package cli_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/creack/pty"
)

// The CLI offers an interactive step the terminal it actually has.
//
// The interpreter accepts `RUN --interactive` only when told a terminal exists
// (E195), and the executor hands over that same terminal. If the CLI passed
// neither, the capability would work in tests and nowhere else - which is the
// shape this work has found five times.
//
// A dry run proves the wiring whichever way the environment falls. **Without the
// option the build is refused always**; with it, the outcome follows this
// process's own terminal - so either branch says the option was passed, and the
// branch taken says which world the test is running in.
func TestTheCLIPassesTheTerminalItHas(t *testing.T) {
	t.Parallel()

	dir := project(t, `VERSION 0.8

main:
    FROM alpine:3.22
    RUN --interactive sh
`, nil)

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "main", Out: &out, DryRun: true,
	})

	// The same question the CLI asks, asked the same way: a controlling terminal
	// is one that /dev/tty opens.
	tty, openErr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if openErr == nil {
		_ = tty.Close()
	}

	// Which world this run is in, in the output: the two branches assert
	// opposite things and a bare PASS does not say which was checked.
	t.Logf("controlling terminal: %v", openErr == nil)

	if openErr == nil {
		if err != nil {
			t.Errorf("this process has a terminal and the plan was refused anyway,"+
				" so the CLI is not offering it:\n%v", err)
		}

		return
	}

	if err == nil {
		t.Fatal("this process has no terminal and an interactive step planned" +
			" anyway, so nothing checked")
	}

	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("refused for something other than the missing terminal:\n%v", err)
	}
}

// And with a terminal, the CLI finds one.
//
// The test above runs under `go test`, which has no controlling terminal, so it
// only ever exercises the refusal - a fact its own output now states rather than
// leaves to be assumed. This is the other half, and it needs a process that
// genuinely has a terminal, which cannot be arranged from inside one that does
// not.
//
// The child is this binary with a pty as its controlling terminal: `Setsid` for
// a new session and `Setctty` to claim it, which is the pair `AttachTerminal`
// uses for a step (E190).
func TestTheCLIFindsATerminalWhenThereIsOne(t *testing.T) {
	if os.Getenv("EARTH_TEST_TTY_CHILD") != "" {
		if cli.HasCallersTerminal() {
			os.Exit(0)
		}

		os.Exit(7)
	}

	t.Parallel()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}

	t.Cleanup(func() { _ = ptmx.Close() })

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot find this test binary: %v", err)
	}

	cmd := exec.CommandContext(t.Context(), self, "-test.run", "^TestTheCLIFindsATerminalWhenThereIsOne$") //nolint:gosec // this binary
	cmd.Env = append(os.Environ(), "EARTH_TEST_TTY_CHILD=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}

	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	_ = tty.Close()

	err = cmd.Wait()
	if err != nil {
		t.Errorf("a process with a controlling terminal did not find one: %v", err)
	}
}
