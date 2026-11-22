package interp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `HOST name ip` makes a name resolve to an address inside every step after it.
//
// Ambient state a step observes, so it belongs to the step rather than to the
// build: two steps in one target may have different entries, and a step's
// entries are part of what it is. `curl http://api.test` with `HOST api.test
// 10.0.0.1` and without it are two different commands wearing the same words.
func TestHostEntriesReachTheStepsAfterThem(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    RUN before
    HOST api.test 10.0.0.1
    HOST db.test 10.0.0.2
    RUN after
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var before, after *ir.Node

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		switch {
		case slices.Contains(n.Op.Args, "before"):
			before = n
		case slices.Contains(n.Op.Args, "after"):
			after = n
		}
	}

	if before == nil || after == nil {
		t.Fatal("the two steps are not both in the plan")
	}

	if len(before.Op.Hosts) != 0 {
		t.Errorf("a step before the HOST lines has entries: %v", before.Op.Hosts)
	}

	want := []string{"api.test 10.0.0.1", "db.test 10.0.0.2"}
	if !slices.Equal(after.Op.Hosts, want) {
		t.Errorf("the step after them has %v, want %v", after.Op.Hosts, want)
	}
}

// A hostname that resolves differently is a different step.
//
// The entries decide what a name resolves to, so a step that fetched from
// `api.test` fetched from whatever the entry pointed at. Two builds with
// different entries that share a key would serve one build's download to the
// other (I3).
func TestChangingAHostEntryChangesTheKey(t *testing.T) {
	t.Parallel()

	key := func(ip string) ir.NodeID {
		t.Helper()

		plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    HOST api.test `+ip+`
    RUN curl api.test
`, "build")
		if err != nil {
			t.Fatalf("%v", err)
		}

		return plan.Graph.Root.ID()
	}

	if key("10.0.0.1") == key("10.0.0.2") {
		t.Error("two steps resolving one name to different addresses share a key")
	}
}

// The arguments are checked, because a typo here is a silent misdirection.
//
// `HOST api.test` with no address, or an address that is not one, would
// otherwise be written into the step's hosts file and make the name resolve to
// nothing - or to something else - with no error anywhere.
func TestAHostEntryIsCheckedWhereItIsWritten(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		line string
		says string
	}{
		{"no address", "HOST api.test", "address"},
		{"not an address", "HOST api.test not-an-ip", "not-an-ip"},
		{"too many arguments", "HOST api.test 10.0.0.1 extra", "extra"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build("VERSION 0.8\nbuild:\n    FROM alpine\n    "+
				tc.line+"\n    RUN true\n", "build")
			if err == nil {
				t.Fatalf("%q was accepted", tc.line)
			}

			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not mention %q: %v", tc.says, err)
			}
		})
	}
}
