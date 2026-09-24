package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A shared inner daemon cache makes the block uncacheable, and says so.
//
// **Sharing and cacheability are one axis, not two.** `WITH DOCKER --cache-id`
// gives the inner daemon storage that survives the block and is shared with
// every other block naming it - so what a step in that block produces depends
// on what some earlier build left behind, which is the definition of a step
// that is not a function of its inputs (I3).
//
// That is also the answer to wanting isolation: a block with no `--cache-id`
// starts with an empty daemon, is reproducible, and is cached. Testing the
// engine's own cache behaviour wants exactly that, and it is the default rather
// than a flag to remember.
func TestASharedDockerCacheMakesTheBlockUncacheable(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    WITH DOCKER --cache-id=shared
        RUN docker images
    END
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var seen int

	for _, n := range dockerSteps(plan) {
		seen++

		if !n.Op.NoCache {
			t.Errorf("a step sharing a docker cache is cacheable; what it"+
				" produces depends on what an earlier build left in that cache"+
				" (%s)", n.Meta.Source)
		}

		if n.Op.DockerCache != "shared" {
			t.Errorf("the step does not name the cache it shares: %q",
				n.Op.DockerCache)
		}
	}

	if seen == 0 {
		t.Fatal("no step in the block was given a daemon")
	}
}

// A block naming no cache still names no cache - and is no longer cacheable for
// it.
//
// **This assertion was reversed on 2026-08-19**, and deliberately. It used to
// read "a block with no shared cache is cacheable, and starts empty", which was
// true while a bare `WITH DOCKER` always got an empty daemon of its own. Sharing
// is now the default (E381): a bare block may be handed the daemon of an outer
// step, so its result is not a function of its inputs and is not reused.
//
// What survives unchanged is the other half - naming no cache still means naming
// no cache, and `--isolate` is what buys the cacheability back. Kept as one test
// so the reversal is visible rather than deleted.
func TestABlockNamingNoCacheStillNamesNone(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    WITH DOCKER
        RUN docker images
    END
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	for _, n := range dockerSteps(plan) {
		if !n.Op.NoCache {
			t.Errorf("a block that may share an outer daemon is cacheable (%s)",
				n.Meta.Source)
		}

		if n.Op.DockerCache != "" {
			t.Errorf("a block that shares nothing names a cache: %q",
				n.Op.DockerCache)
		}
	}
}

// Two blocks naming different caches are different steps.
//
// In the key for the reason `Docker` is: a daemon holding one project's images
// and a daemon holding another's answer `docker images` differently, and a cache
// that could not tell them apart would serve one for the other.
func TestTwoDockerCachesAreTwoDifferentSteps(t *testing.T) {
	t.Parallel()

	one := dockerStep(t, "a")
	two := dockerStep(t, "b")

	if one == two {
		t.Error("two blocks sharing different daemon caches key the same")
	}
}

func dockerStep(t *testing.T, id string) ir.NodeID {
	t.Helper()

	plan, err := interp.Build(strings.ReplaceAll(`
VERSION 0.8
build:
    FROM alpine
    WITH DOCKER --cache-id=ID
        RUN docker images
    END
`, "ID", id), "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	if got := dockerSteps(plan); len(got) > 0 {
		return got[0].ID()
	}

	t.Fatal("no docker step")

	return ir.NodeID{}
}

// dockerSteps is every step in a plan that was given a daemon.
func dockerSteps(p *interp.Plan) []*ir.Node {
	var out []*ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && n.Op.Docker {
			out = append(out, n)
		}
	}

	return out
}

// A block's cache is its own, and does not leak past its END.
//
// **The inception case.** A `--load` builds another target, and that target may
// have a `WITH DOCKER` of its own - so one block is planned while another is
// open. The cache is held on the plan for the length of a block, and the first
// version cleared it at the end rather than restoring what was there, so an
// outer block lost its own cache the moment an inner one finished (E356).
//
// Same shape, no `--load` needed to provoke it: two blocks in sequence, the
// second sharing nothing. If clearing were restoring, the second would find the
// first's cache still set.
func TestABlocksCacheDoesNotLeakPastItsEnd(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    WITH DOCKER --cache-id=first
        RUN docker images
    END
    WITH DOCKER
        RUN docker ps
    END
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var shared, isolated int

	for _, n := range dockerSteps(plan) {
		switch n.Op.DockerCache {
		case "first":
			shared++
		case "":
			isolated++
		default:
			t.Errorf("a step names cache %q, which no block asked for",
				n.Op.DockerCache)
		}
	}

	if shared == 0 {
		t.Error("the first block's steps do not name its cache")
	}

	if isolated == 0 {
		t.Error("the second block shares nothing and no step says so" +
			"\n  a cache that outlives its block makes every later block" +
			" uncacheable for a reason its author did not write (E356)")
	}
}

// A block inside a block does not take the outer one's cache with it.
//
// **Inception, and the case the sequential test cannot reach.** A `--load`
// builds another target *while this block is open*, and that target may have a
// `WITH DOCKER` of its own. The cache is held on the plan for the length of a
// block and was cleared at the end rather than restored - so the inner block's
// END emptied the outer block's, and every step of the outer planned afterwards
// claimed to share nothing while running against a daemon that shares
// everything (E356).
//
// Wrong in the direction that matters: those steps become **cacheable**, and
// what they see is another build's images.
func TestAnInnerBlockDoesNotTakeTheOuterCacheWithIt(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
inner:
    FROM alpine
    WITH DOCKER
        RUN docker ps
    END
    SAVE IMAGE inner:latest

build:
    FROM alpine
    WITH DOCKER --cache-id=outer --load=inner:latest=+inner
        RUN docker images
    END
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	// **Every** step of the outer block, by the line it came from - not only the
	// authored one. The steps this engine generates are where the leak showed:
	// the body reads a local, and a `--load` or a cleanup reads the plan.
	var outer int

	for _, n := range dockerSteps(plan) {
		if !strings.HasSuffix(n.Meta.Source, ":12") &&
			!strings.HasSuffix(n.Meta.Source, ":13") {
			continue
		}

		outer++

		if n.Op.DockerCache != "outer" {
			t.Errorf("a step of the outer block names cache %q after an inner"+
				" block closed - it is now cacheable and reads a daemon that"+
				" shares everything (E356)\n  %s", n.Op.DockerCache,
				n.Meta.Description)
		}
	}

	if outer == 0 {
		t.Fatal("the outer block's steps are not in the graph")
	}
}

// A cache id is a name, and is checked before anything is named after it.
//
// **It became an input last iteration** (E354), and it is the kind that ends up
// in a path: a shared daemon's storage has to live somewhere, and where is
// derived from the id. `--cache-id=../../etc` would be a directory traversal in
// a mount that does not exist yet, which is the best moment to refuse it - at
// the line that wrote it, in the interpreter, where a refusal names the file and
// the column (I10).
//
// Checked here rather than at the mount for the same reason `--platform` is: the
// executor's refusal would name a path this engine composed, and the author
// wrote a flag.
//
// An **empty** id is not in this list: `--cache-id=` names no cache, which is
// the isolated default said out loud, and refusing it would refuse a way of
// writing the thing that already works.
//
// Nor is a name with a space in it, and that is a different fault with its own
// test below: the line parses as a cache called `with` and a stray word, and the
// stray word was being discarded.
func TestACacheIDIsCheckedWhereItIsWritten(t *testing.T) {
	t.Parallel()

	for _, id := range []string{
		"../escape", "a/b", "/absolute", ".", "..",
		strings.Repeat("x", 200),
	} {
		_, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    WITH DOCKER --cache-id=`+id+`
        RUN docker images
    END
`, "build")
		if err == nil {
			t.Errorf("--cache-id=%q was accepted; it names a directory", id)

			continue
		}

		if !strings.Contains(err.Error(), "--cache-id") {
			t.Errorf("--cache-id=%q was refused without naming the flag:\n%s",
				id, err)
		}
	}
}

// The names people actually use are accepted.
//
// A refusal that took the reasonable cases with the dangerous ones would be a
// worse defect than the one it prevents: nobody would reach for the flag at all,
// and the shared cache that most uses want would be unreachable.
func TestOrdinaryCacheIDsAreAccepted(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"layers", "my-cache", "cache_1", "Build.2"} {
		_, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    WITH DOCKER --cache-id=`+id+`
        RUN docker images
    END
`, "build")
		if err != nil {
			t.Errorf("--cache-id=%q was refused: %v", id, err)
		}
	}
}

// WITH DOCKER takes flags and nothing else.
//
// **Anything left over was discarded.** `WITH DOCKER --cache-id=with space`
// parses as a cache called `with` and a word nobody looked at, so an author who
// wrote a name with a space in it got a cache with a different name and no
// indication. Accepted-and-ignored is the failure every option refusal in this
// construct exists to prevent, and it was reachable past all of them (E358).
func TestWithDockerTakesNoArgumentsOfItsOwn(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"WITH DOCKER --cache-id=with space",
		"WITH DOCKER something",
	} {
		_, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    `+line+`
        RUN docker images
    END
`, "build")
		if err == nil {
			t.Errorf("%q was accepted and the extra word discarded", line)
		}
	}
}
