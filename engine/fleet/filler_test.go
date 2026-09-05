package fleet_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A path the base has is fetched and placed where the step is looking.
//
// The two ends joined: the tracer stops a step before an open (E289), this works
// out which layer the path is in, asks a peer for that one path (E288), and puts
// it where the step will find it (E290).
func TestAPathTheBaseHasIsPlacedWhereTheStepIsLooking(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)

	into := t.TempDir()

	f := &fleet.Filler{
		Into:  into,
		Stack: []ir.NodeID{id},
		From:  []fleet.Fragmenter{&fromStore{layers: &fleet.Layers{Root: theirs}}},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	err := f.Fill(context.Background(), filepath.Join(into, "etc", "hosts"))
	if err != nil {
		t.Fatalf("filling: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(into, "etc", "hosts"))
	if err != nil {
		t.Fatalf("the file is not where the step is looking: %v", err)
	}

	if string(body) != "127.0.0.1 localhost\n" {
		t.Errorf("it arrived as %q", body)
	}
}

// A path no layer has is absent, and saying so is not an error.
//
// **The distinction E289 turns on.** A step looking for a file its base does not
// contain must get an honest ENOENT; a step looking for one this engine could
// not *reach* must not. The protocol tells them apart already: a fragment that
// arrives without the path is a layer that does not have it.
func TestAPathNoLayerHasIsAbsentRatherThanAnError(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)

	into := t.TempDir()

	f := &fleet.Filler{
		Into:  into,
		Stack: []ir.NodeID{id},
		From:  []fleet.Fragmenter{&fromStore{layers: &fleet.Layers{Root: theirs}}},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	err := f.Fill(context.Background(), filepath.Join(into, "etc", "not-here"))
	if err != nil {
		t.Fatalf("a file the base does not have was reported as a failure: %v"+
			"\n  the step would be failed for reading something that is"+
			" honestly not there", err)
	}

	_, err = os.Lstat(filepath.Join(into, "etc", "not-here"))
	if err == nil {
		t.Error("something was created for a path no layer has")
	}
}

// A path nobody could be asked about is an error, so the step fails.
//
// The other side of the same coin, and the one that prevents a wrong build: this
// engine could not find out whether the file exists, so it must not let the step
// conclude that it does not.
func TestAPathNobodyCouldBeAskedAboutFailsTheStep(t *testing.T) {
	t.Parallel()

	into := t.TempDir()

	f := &fleet.Filler{
		Into:  into,
		Stack: []ir.NodeID{{1}},
		From:  []fleet.Fragmenter{&nothing{}},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	err := f.Fill(context.Background(), filepath.Join(into, "etc", "hosts"))
	if err == nil {
		t.Fatal("an unreachable base was reported as a base without the file" +
			"\n  the step takes the other branch and produces a layer keyed as" +
			" though the file were absent")
	}
}

// A path in two layers comes from the upper one.
//
// A stack is a stack: the layer nearest the top wins, because that is what the
// step would see if the whole thing were materialised. A filler that took the
// first answer it got would hand the step a file the base has overwritten.
func TestAPathInTwoLayersComesFromTheUpperOne(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	lower := aLayerWithFile(t, store, "etc/hosts", "the older one\n")
	upper := aLayerWithFile(t, store, "etc/hosts", "the newer one\n")

	into := t.TempDir()

	f := &fleet.Filler{
		Into: into,
		// Base first is bottom first, as a stack is written.
		Stack: []ir.NodeID{lower, upper},
		From:  []fleet.Fragmenter{&fromStore{layers: &fleet.Layers{Root: store}}},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	err := f.Fill(context.Background(), filepath.Join(into, "etc", "hosts"))
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(into, "etc", "hosts"))
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "the newer one\n" {
		t.Errorf("got %q; the upper layer's copy is what the step would see", body)
	}
}

// A path outside the base is not this filler's business.
//
// A step reads `/proc`, `/dev`, and its own working directory. Asking a peer for
// those would be a fetch that cannot succeed and a step failed for reading
// something perfectly ordinary.
func TestAPathOutsideTheBaseIsLeftAlone(t *testing.T) {
	t.Parallel()

	f := &fleet.Filler{
		Into:  t.TempDir(),
		Stack: []ir.NodeID{{1}},
		From:  []fleet.Fragmenter{&nothing{}},
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	err := f.Fill(context.Background(), "/proc/self/status")
	if err != nil {
		t.Errorf("a path outside the base was treated as one inside it: %v", err)
	}
}

// aLayerWithFile writes a one-file layer into a store.
func aLayerWithFile(t *testing.T, root, path, body string) ir.NodeID {
	t.Helper()

	tmp := t.TempDir()

	must(t, os.MkdirAll(filepath.Join(tmp, filepath.Dir(path)), 0o750))
	must(t, os.WriteFile(filepath.Join(tmp, path), []byte(body), 0o600))

	c, err := layer.Take(tmp)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(root, "layers", c.ID.String())
	must(t, os.MkdirAll(filepath.Dir(at), 0o750))
	must(t, os.Rename(tmp, at))

	return c.ID
}
