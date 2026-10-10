package fleet_test

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// TestAFleetCanMoveADeclaration.
//
// **A stack element need not be a layer.** An image that contributes only
// configuration - environment, working directory, user, entrypoint - is held as
// a declaration: a file of a couple of hundred bytes beside the layers rather
// than a directory among them. The materialiser knows that and asks for either.
//
// The fleet did not. `Layers.Has` stats the layer directory and requires
// `IsDir`, so a driver holding a declaration reported that it held nothing,
// offered no source for it, and every worker refused every step standing on it:
//
//	1 of 2 input(s) for a delegated step: some blobs could not be fetched
//	  first 5623a794…, and no source was consulted at all
//
// Measured on a two-machine fleet: six delegated steps, six refusals, a
// gigabyte moved for nothing and the driver running the whole build itself
// (E-F1). Cheap to fix and cheap to send - a declaration is smaller than the
// message complaining about it.
func TestAFleetCanMoveADeclaration(t *testing.T) {
	t.Parallel()

	theirs := t.TempDir()

	want := decl.Declaration{
		Env:        []string{"PATH=/usr/bin", "RUSTUP_HOME=/usr/local/rustup"},
		WorkingDir: "/w",
		User:       "root",
		Entrypoint: []string{"/bin/sh"},
	}

	id, err := decl.Write(theirs, want)
	if err != nil {
		t.Fatalf("filing a declaration: %v", err)
	}

	from := &fleet.Layers{Root: theirs}

	if !from.Has(id) {
		t.Fatal("a store holding a declaration reports that it holds nothing," +
			" so nothing is ever asked for it and every step standing on it is" +
			" refused")
	}

	packed, err := from.Get(id)
	if err != nil {
		t.Fatalf("packing a declaration: %v", err)
	}

	mine := t.TempDir()

	got, _, err := (&fleet.Layers{Root: mine}).Put(bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("receiving a declaration: %v", err)
	}

	// **Named by its contents at both ends**, which is what makes it safe to
	// accept: a peer that sent something else produces a different identity and
	// `Provision` refuses it.
	if got != id {
		t.Fatalf("a declaration arrived as %v, sent as %v", got, id)
	}

	back, held, err := decl.Read(mine, id)
	if err != nil || !held {
		t.Fatalf("reading it back: held=%v err=%v", held, err)
	}

	if back.WorkingDir != want.WorkingDir || back.User != want.User ||
		len(back.Env) != len(want.Env) || len(back.Entrypoint) != len(want.Entrypoint) {
		t.Errorf("it arrived as %+v, sent as %+v", back, want)
	}
}
