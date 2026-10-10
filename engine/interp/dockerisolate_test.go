package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `--isolate` gives the block a daemon of its own, and that is what makes it
// cacheable.
//
// Sharing is the default (E381), so a bare block may be handed a daemon an outer
// step has been using and its result is not a function of its inputs. `--isolate`
// is the opt-out, and the only mode whose result can be reused: the daemon's
// storage lives in the step's own overlay and dies with it, because nothing is
// mounted (E365).
func TestAnIsolatedDockerBlockIsTheCacheableOne(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    WITH DOCKER --isolate
        RUN docker images
    END
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var seen int

	for _, n := range dockerSteps(plan) {
		seen++

		if !n.Op.IsolateDocker {
			t.Errorf("the step was not marked isolated, so it will be given"+
				" whatever daemon is around it (%s)", n.Meta.Source)
		}

		if n.Op.NoCache {
			t.Errorf("an isolated block is uncacheable; its daemon starts empty"+
				" and dies with the step, so its result is a function of its"+
				" inputs (%s)", n.Meta.Source)
		}
	}

	if seen == 0 {
		t.Fatal("no step in the block was given a daemon")
	}
}

// A block that says nothing may share, so it is not cached.
//
// **This reverses what the engine did before the decision of 2026-08-19.** A
// bare block used to start an empty daemon and was cacheable on that basis. With
// sharing as the default it may be handed an outer step's daemon, and a result
// that depends on what some other build left behind is not one to reuse (I3).
//
// The author gets the sharing they wanted by default and pays for it in
// cacheability rather than in correctness - and gets both back by writing
// `--isolate`, which is precisely the case that no longer needs the sharing.
func TestABlockThatMaySharePaysForItInCacheability(t *testing.T) {
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

	var seen int

	for _, n := range dockerSteps(plan) {
		seen++

		if n.Op.IsolateDocker {
			t.Errorf("a block that asked for nothing was isolated (%s)", n.Meta.Source)
		}

		if !n.Op.NoCache {
			t.Errorf("a block that may share an outer daemon is cacheable; what"+
				" it produces depends on what that daemon already had (%s)",
				n.Meta.Source)
		}
	}

	if seen == 0 {
		t.Fatal("no step in the block was given a daemon")
	}
}

// The two options contradict each other and saying both is refused.
//
// `--isolate` says the storage dies with the step; `--cache-id` names storage
// that outlives it. An engine that honoured one and ignored the other would be
// doing something the author did not ask for either way (I10).
func TestIsolateAndACacheIDAreRefusedTogether(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    WITH DOCKER --isolate --cache-id=shared
        RUN docker images
    END
`, "build")
	if err == nil {
		t.Fatal("a block asking for its own daemon and for shared storage was accepted")
	}

	for _, want := range []string{"--isolate", "--cache-id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}
