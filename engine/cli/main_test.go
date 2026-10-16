package cli_test

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/guestd"
)

// TestMain dispatches the engine's re-exec entry points before running tests.
//
// **Because the engine re-executes `os.Executable()`, and under `go test` that
// is this binary.** A microVM's network shim is a second entry point into
// whatever started it: the CLI dispatches it in `cmd/earth/main.go` and gets a
// tap in a new namespace, and a test binary that did not dispatch it got its
// own `TestMain` instead - which ran the whole corpus gate again, inside the
// build the gate had just started.
//
// It was not subtle in its effects and said nothing about its cause: 88 test
// processes, 280 attempts at a 246-invocation corpus, and 128 refusals reading
// `the store device is in use by another build` - each of them true, the other
// build being this one. The measurement it produced, 67 of 246 against the 194
// namespaces manage, was not a measurement of anything.
//
// TestEveryShimIsDispatchedWhereverThisBinaryIsReExecuted holds this file and
// cmd/earth/main.go to the same list, read from where the commands are
// declared, so the next shim cannot be added to only one of them.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == guestd.Command {
		guestd.Main(os.Args[2:])

		return
	}

	if len(os.Args) > 1 && os.Args[1] == exec.NetShimCommand {
		exec.NetShimMain(os.Args[2:])

		return
	}

	if len(os.Args) > 1 && os.Args[1] == exec.NetFDCommand {
		exec.NetFDMain(os.Args[2:])

		return
	}

	os.Exit(m.Run())
}
