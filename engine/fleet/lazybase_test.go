package fleet_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A lazily materialised base serves a step that reads beyond its prediction.
//
// Everything joined, and the loop closed: the predicted paths are here before
// the step starts, the one it was not predicted to read is faulted in while it
// runs, and the rest of the base never moves (E292).
func TestALazyBaseServesAStepThatReadsBeyondItsPrediction(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)

	src := &fromStore{layers: &fleet.Layers{Root: theirs}}

	base := t.TempDir()

	f := &fleet.Filler{
		Into:  base,
		Stack: []ir.NodeID{id},
		From:  []fleet.Fragmenter{src},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	// What the driver predicted this step would read.
	err := f.Prime(context.Background(), []string{"etc/hosts"})
	if err != nil {
		t.Fatalf("priming the base: %v", err)
	}

	primed := src.asked

	// The step reads what was predicted, and finds it without asking anybody.
	body, err := os.ReadFile(filepath.Join(base, "etc", "hosts"))
	if err != nil {
		t.Fatalf("a predicted path was not in the primed base: %v", err)
	}

	if string(body) != "127.0.0.1 localhost\n" {
		t.Errorf("the predicted path arrived as %q", body)
	}

	if src.asked != primed {
		t.Errorf("reading a predicted path cost %d fetch(es)",
			src.asked-primed)
	}

	// And then it reads something nobody predicted. The tracer would stop it
	// here; this is what the tracer calls.
	unpredicted := filepath.Join(base, "usr", "lib", "lib7.so")

	if _, err := os.Lstat(unpredicted); err == nil {
		t.Fatal("the whole layer was materialised; there is nothing lazy about" +
			" this")
	}

	err = f.Fill(context.Background(), unpredicted)
	if err != nil {
		t.Fatalf("faulting in an unpredicted path: %v", err)
	}

	_, err = os.Stat(unpredicted)
	if err != nil {
		t.Fatalf("the unpredicted path did not arrive: %v", err)
	}

	// The rest of the base is still not here, which is the entire point.
	absent := 0

	for i := range 40 {
		p := filepath.Join(base, "usr", "lib", "lib"+itoa(i)+".so")
		_, err := os.Lstat(p)
		if err != nil {
			absent++
		}
	}

	t.Logf("%d of 40 library files never moved", absent)

	if absent < 30 {
		t.Errorf("only %d of 40 files stayed away", absent)
	}
}

// A step that reads beyond its prediction with nobody to ask is failed.
//
// The safety property, composed: priming succeeded, so the step is running - and
// then the peer goes away. The fault-in must fail rather than let the step
// conclude the file is not in its base (E289).
func TestAStepReadingBeyondItsPredictionWithNobodyToAskIsFailed(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)

	base := t.TempDir()

	// Primed from a source that then goes away.
	good := &fromStore{layers: &fleet.Layers{Root: theirs}}

	f := &fleet.Filler{
		Into:  base,
		Stack: []ir.NodeID{id},
		From:  []fleet.Fragmenter{good},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	err := f.Prime(context.Background(), []string{"etc/hosts"})
	if err != nil {
		t.Fatal(err)
	}

	f.From = []fleet.Fragmenter{&nothing{}}

	err = f.Fill(context.Background(), filepath.Join(base, "usr", "lib", "lib7.so"))
	if err == nil {
		t.Fatal("a step was told a file is not in its base by a peer that could" +
			" not be reached")
	}
}

// Priming with no prediction materialises nothing.
//
// A worker with no prediction has to fetch the whole layer the ordinary way -
// and an empty prime must not look like a base, because a step would then find
// nothing and fault on every path it opened.
func TestPrimingWithNoPredictionMaterialisesNothing(t *testing.T) {
	t.Parallel()

	src := &nothing{}

	f := &fleet.Filler{
		Into:  t.TempDir(),
		Stack: []ir.NodeID{{1}},
		From:  []fleet.Fragmenter{src},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	err := f.Prime(context.Background(), nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if src.asked != 0 {
		t.Errorf("asked %d time(s) with nothing predicted", src.asked)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var b []byte

	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}

	return string(b)
}

// Priming a stack leaves the upper layer's copy in place.
//
// `Prime` writes bottom up where `Fill` searches top down, and both have to
// reach the same answer: what a step sees is what the whole stack materialised
// would show it. A prime that stopped at the first layer with the path would
// leave the step reading a file its base has overwritten - and the step would
// succeed, with a layer keyed as though it had read the current one.
func TestPrimingAStackLeavesTheUpperLayersCopy(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	lower := aLayerWithFile(t, store, "etc/hosts", "the older one\n")
	upper := aLayerWithFile(t, store, "etc/hosts", "the newer one\n")

	base := t.TempDir()

	f := &fleet.Filler{
		Into:  base,
		Stack: []ir.NodeID{lower, upper},
		From:  []fleet.Fragmenter{&fromStore{layers: &fleet.Layers{Root: store}}},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	err := f.Prime(context.Background(), []string{"etc/hosts"})
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(base, "etc", "hosts"))
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "the newer one\n" {
		t.Errorf("got %q; the upper layer's copy is what the step would see", body)
	}
}
