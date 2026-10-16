package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A bound view of the local context plans, and what it shows reaches the key.
//
// Green paper §3.3d with ν = 𝑐. The second half is the one that matters: a
// cache mount's contents are deliberately outside the key, and a reader who
// carried that habit across would give a bound view the same treatment - at
// which point editing the bound file leaves the key alone and the step is
// served a result produced from different bytes. That is the false hit I3
// exists to forbid, and I20 is the rule that stops it.
func TestABoundViewOfTheContextIsKeyedByWhatItShows(t *testing.T) {
	t.Parallel()

	const src = `
main:
    FROM alpine:3.22
    RUN --mount=type=bind,source=data,target=/data cat /data/f
`

	first := keyOfRun(t, src, "one")
	again := keyOfRun(t, src, "one")
	edited := keyOfRun(t, src, "two")

	if first != again {
		t.Error("the same context planned two different keys, so nothing" +
			" bound would ever hit the cache")
	}

	if first == edited {
		t.Error("editing the bound file left the key alone: the step would be" +
			" served a result produced from different bytes (I3, I20)")
	}
}

// The object it shows is a source of the step, not an input.
//
// A source decides the result and reaches the key without being stacked
// underneath - which is exactly what a bound view is. Stacking it would merge
// the context into the step's filesystem, which is COPY's job and not this one.
func TestABoundViewIsASourceRatherThanAnInput(t *testing.T) {
	t.Parallel()

	dir := contextHolding(t, "data/f", "hello")

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN --mount=type=bind,source=data,target=/data cat /data/f
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatalf("a bound view of the context was refused: %v", err)
	}

	run := runNode(t, p)

	if len(run.Sources) != 1 {
		t.Fatalf("the step has %d sources; the object it binds must be one,"+
			" or nothing builds it and nothing keys it", len(run.Sources))
	}

	if len(run.Op.Mounts) != 1 || run.Op.Mounts[0].From != run.Sources[0].ID() {
		t.Errorf("the mount does not name the source it shows: %+v", run.Op.Mounts)
	}

	for _, in := range run.Inputs {
		if in == run.Sources[0] {
			t.Error("the bound object is stacked underneath the step as well," +
				" which merges the context in - that is COPY's job")
		}
	}
}

// A view of an earlier stage is refused, and says why rather than "no".
//
// It is unbuilt rather than declined: a stage's filesystem is an assembled
// stack of layers and not one layer, so showing it needs machinery a view of
// the context does not. Saying so is the difference between somebody picking
// the work up and somebody assuming it was decided against.
func TestAViewOfAnEarlierStageSaysItIsUnbuilt(t *testing.T) {
	t.Parallel()

	dir := contextHolding(t, "data/f", "hello")

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN --mount=type=bind,from=other,source=/x,target=/x true
`, testMain, interp.WithContext(dir))
	if err == nil {
		t.Fatal("a view of another stage was accepted")
	}

	if strings.Contains(err.Error(), "design") {
		t.Errorf("refused as a decision; nothing has been decided about it: %v", err)
	}

	if !strings.Contains(err.Error(), "from") {
		t.Errorf("the refusal does not name what was not honoured: %v", err)
	}
}

// contextHolding is a build context with one file in it.
func contextHolding(t *testing.T, at, body string) string {
	t.Helper()

	dir := t.TempDir()

	err := os.MkdirAll(filepath.Dir(filepath.Join(dir, at)), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, at), []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// keyOfRun plans src against a context holding body, and returns the RUN's id.
func keyOfRun(t *testing.T, src, body string) ir.NodeID {
	t.Helper()

	p, err := interp.Build(versioned+src, testMain,
		interp.WithContext(contextHolding(t, "data/f", body)))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	return runNode(t, p).ID()
}

// runNode is the plan's only OpExec node.
func runNode(t *testing.T, p *interp.Plan) *ir.Node {
	t.Helper()

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			return n
		}
	}

	t.Fatal("the plan has no step to look at")

	return nil
}
