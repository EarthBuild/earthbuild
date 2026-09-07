package exec_test

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/creack/pty"
)

// A real guest process gives an interactive step the caller's terminal.
//
// E192 joined the pieces inside one process. This is the arrangement that
// matters: `earth-guestd` is a separate program, started by the engine and
// spoken to over pipes, so the terminal travels on a channel of its own - named
// by `EARTH_GUEST_TERMINALS` rather than counted, because the id gate takes fd 3
// only on the ranged path and a positional number would move underneath it.
//
// Through the public path, so what is tested is what a build does: the executor
// holds the terminal, the node says it is interactive, and `Run` puts the two
// together. A step that did not ask gets nothing, which is why the terminal is
// not simply handed to every step.
//
// The assertion is `/dev/tty`, which is what a controlling terminal *is*. A step
// whose streams merely point at a pty passes `test -t 0` and has no job control,
// and that difference is why this construct needs a descriptor rather than a
// relay.
func TestARealGuestGivesAStepTheCallersTerminal(t *testing.T) {
	t.Parallel()

	sb := exec.NewNative()

	err := sb.Available()
	if err != nil {
		t.Skipf("native backend unavailable: %v", err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}

	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })

	e.Terminal = tty

	base := putProbeLayerAt(t, sb.StoreDir())

	said := make(chan string, 4)

	go func() {
		sc := bufio.NewScanner(ptmx)
		for sc.Scan() {
			said <- strings.TrimSpace(sc.Text())
		}
	}()

	done := make(chan error, 1)

	go func() {
		n := &ir.Node{
			Op: ir.Op{
				Kind: ir.OpExec, Args: []string{"/probe", "ctty"},
				Interactive: true,
			},
			Inputs: []*ir.Node{base},
			Meta:   ir.Meta{Source: "Earthfile:1", Description: "RUN --interactive"},
		}

		_, runErr := e.Run(context.Background(), n, core.Worker{ID: testNative},
			[]ir.NodeID{base.ID()}, nil)
		done <- runErr
	}()

	select {
	case l := <-said:
		if l != "HAS-CTTY" {
			t.Errorf("the step said %q on the terminal it was handed", l)
		}
	case <-time.After(60 * time.Second):
		select {
		case runErr := <-done:
			t.Fatalf("nothing on the terminal; the step said: %v", runErr)
		default:
			t.Fatal("nothing on the terminal, and the step has not returned")
		}
	}
}
