package check_test

import (
	"os"
	"strings"
	"testing"
)

// reExecs are the entry points a front end is re-executed through, and what
// each one is for.
//
// **A binary that starts a microVM has four doors, not one.** Each is the same
// arrangement: something needs a namespace it cannot enter after the fact, Go
// cannot run code between clone and exec, so the engine re-executes its own
// binary with a command word in front. A front end that does not dispatch one
// of them parses the re-exec's arguments as a build's, prints a usage message
// where a tap or an agent was expected, and fails somewhere a long way from
// here.
var reExecs = map[string]string{
	"guestd.Command":       "the sandbox agent, which on Linux is this binary",
	"RunStepShimIfAsked":   "a step's own namespaces",
	"RunDaemonShimIfAsked": "a step's own docker daemon",
	"exec.NetShimCommand":  "the microVM's tap, made in a namespace of its own",
	"exec.NetFDCommand":    "sockets on that tap, for a build that finds the machine running",
}

// frontEnds are the binaries that can reach a sandbox, and so need every door.
var frontEnds = []string{
	"../../cmd/earth/main.go",
	"../../cmd/earth-native/main.go",
}

// TestEveryFrontEndDispatchesEveryReExec.
//
// `earth-native` dispatched three of the four and missed the microVM's network
// shim, so `EARTH_VM=1` through that binary re-executed it with `vm-net` as the
// target: it answered `"/bin/true" is not a build argument`, never made the tap,
// and the engine waited out its ten-second patience and reported "the guest's
// network never arrived". Every build step, and `-prune` with it - which is how
// it was found, because prune is the one operation only that binary offers.
//
// Read as text rather than parsed. The property is that a name appears in a
// file somebody has to remember to edit, which is exactly what a forgotten line
// looks like; a type checker cannot see an omission.
//
// **Not the only guard on this, and the other one is better where they
// overlap.** `engine/cli` has TestEveryShimIsDispatchedWhereverThisBinaryIsReExecuted,
// which discovers the command words by parsing where they are declared instead
// of listing them, so a new one is covered without anybody remembering. It was
// written first and this was written without finding it.
//
// Kept because the two cover different things. That one finds exported consts
// ending in `Command`; two of the re-execs here are functions - the step and
// daemon shims - and no parse of a const list will ever see them. Add a new
// *command* there; add a new *shim function* here.
func TestEveryFrontEndDispatchesEveryReExec(t *testing.T) {
	t.Parallel()

	for _, at := range frontEnds {
		src, err := os.ReadFile(at)
		if err != nil {
			t.Fatalf("read %s: %v", at, err)
		}

		for name, why := range reExecs {
			if strings.Contains(string(src), name) {
				continue
			}

			t.Errorf("%s does not dispatch %s (%s)"+
				"\n  a re-exec this binary does not recognise is read as a build's"+
				" own arguments, and fails as a usage message", at, name, why)
		}
	}
}
