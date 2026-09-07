package fleet_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Every operation the IR has is either delegable or refused, and never guessed.
//
// The IR's vocabulary is larger than the wire's, deliberately (C.3). What the
// type makes impossible is *expressing* the difference; what a test has to make
// impossible is somebody adding a ninth opcode and this conversion quietly
// defaulting it to something.
//
// So every kind is enumerated here with its answer. A new one fails this test
// until a person writes down which it is - which is the same shape as the
// key-coverage guard, and for the same reason: the decision must be made rather
// than inherited.
func TestEveryOpcodeIsDecidedOneWayOrTheOther(t *testing.T) {
	t.Parallel()

	// The whole of ir.OpKind, and the count below is what makes it the whole.
	for _, tc := range []struct {
		kind ir.OpKind
		want fleet.Kind // empty means it must be refused
		why  string
	}{
		{kind: ir.OpImage, want: fleet.KindImage},
		{kind: ir.OpExec, want: fleet.KindExec},
		{kind: ir.OpFile, want: fleet.KindFile},
		{kind: ir.OpBuild, want: fleet.KindBuild},

		{kind: ir.OpHost, why: "runs on the invoking machine"},
		{kind: ir.OpLocal, why: "reads the invoking machine's filesystem"},
		{kind: ir.OpMerge, why: "is not in the wire vocabulary"},
		{kind: ir.OpPackImage, why: "writes into this machine's layer store"},
		// The empty base produces nothing, so shipping it costs a round trip
		// for no work - a decision rather than a gap (E468).
		{kind: ir.OpScratch, why: "produces nothing"},
	} {
		n := &ir.Node{Op: ir.Op{Kind: tc.kind, Args: []string{"x"}}}

		got, err := fleet.Delegate(n, nil, nil)

		if tc.want == "" {
			if !errors.Is(err, fleet.ErrNotDelegable) {
				t.Errorf("%s was delegated (%v); it %s", tc.kind, err, tc.why)
			}

			continue
		}

		if err != nil {
			t.Errorf("%s was refused: %v", tc.kind, err)

			continue
		}

		if got.Op.Kind != tc.want {
			t.Errorf("%s became %q, want %q", tc.kind, got.Op.Kind, tc.want)
		}
	}

	// A tenth opcode has to appear here before it can appear anywhere else. The
	// number is the guard: `OpScratch` is the last, and iota starts at one.
	//
	// It is the last because it was *appended*: an opcode's number is hashed
	// into every key that mentions it, so inserting one renumbers those after it
	// and changes what existing entries were filed under. This guard caught that
	// too, by counting (E468).
	if last := int(ir.OpScratch); last != 9 {
		t.Errorf("the IR has %d opcodes and this test enumerates 9; a new one"+
			" must be decided delegable or refused rather than defaulted", last)
	}
}

// A step this machine cannot describe in an assignment is refused, not trimmed.
//
// `fleet.Op` has no field for a secret, a mount or a docker daemon, so a step
// carrying one cannot be expressed. The tempting answer is to send what fits;
// that would hand a worker a step **missing an input it depends on**, and the
// result would be wrong rather than slow - which is the failure the whole
// engine is built around.
//
// So the poverty of the type is a refusal, not a filter.
func TestAStepThatCannotBeExpressedIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		op   ir.Op
		want string
	}{
		{
			name: "a secret",
			op:   ir.Op{Kind: ir.OpExec, Args: []string{"x"}, SecretEnv: []string{"TOKEN"}},
			want: "secret",
		},
		{
			name: "a mount",
			op: ir.Op{
				Kind: ir.OpExec, Args: []string{"x"},
				Mounts: []ir.Mount{{ID: "m", Target: "/c"}},
			},
			// The cache by name, not the word "mount": a step with five of them
			// is refused for one, and the author is owed which (E433). Stricter
			// than the assertion it replaces, not looser.
			want: "cache m",
		},
		{
			name: "a docker daemon",
			op:   ir.Op{Kind: ir.OpExec, Args: []string{"x"}, Docker: true},
			want: "docker",
		},
		{
			name: "an interactive step",
			op:   ir.Op{Kind: ir.OpExec, Args: []string{"x"}, Interactive: true},
			want: "terminal",
		},
	} {
		_, err := fleet.Delegate(&ir.Node{Op: tc.op}, nil, nil)

		if !errors.Is(err, fleet.ErrNotDelegable) {
			t.Errorf("%s: %v; a step whose inputs cannot be described must be"+
				" refused rather than sent without them", tc.name, err)

			continue
		}

		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refused with %q, which does not say what could not be"+
				" expressed", tc.name, err)
		}
	}
}

// What is delegated carries digests and nothing else.
func TestADelegatedStepCarriesItsBaseAsDigests(t *testing.T) {
	t.Parallel()

	base := []ir.NodeID{{1}, {2}}
	sources := [][]ir.NodeID{{{3}}}

	n := &ir.Node{Op: ir.Op{
		Kind: ir.OpExec, Args: []string{"make"},
		Env: map[string]string{"CC": "gcc"}, Dir: "/src", User: "build",
	}}

	got, err := fleet.Delegate(n, base, sources)
	if err != nil {
		t.Fatal(err)
	}

	if got.Version != fleet.Version {
		t.Errorf("version %d, want %d", got.Version, fleet.Version)
	}

	if len(got.Base) != 2 || got.Base[0] != base[0] {
		t.Errorf("base is %v, want %v", got.Base, base)
	}

	if len(got.Sources) != 1 || got.Sources[0][0] != sources[0][0] {
		t.Errorf("sources are %v, want %v", got.Sources, sources)
	}

	if got.Op.Dir != "/src" || got.Op.User != "build" || got.Op.Env["CC"] != "gcc" {
		t.Errorf("the operation did not survive: %+v", got.Op)
	}
}
