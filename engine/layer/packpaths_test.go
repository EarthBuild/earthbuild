package layer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A fragment carries what was asked for, and the directories to put it in.
//
// Most of a base is never read. Container runtimes answered that with seekable
// layer formats; this engine can do better, because it already knows which paths
// a step read last time and can send those (E281).
//
// The ancestors come too, and not as a convenience: a file cannot be placed
// without the directories above it, and those directories carry modes and
// ownership that are part of what the step will see.
func TestAFragmentCarriesWhatWasAskedForAndItsAncestors(t *testing.T) {
	t.Parallel()

	root := tree(t)

	var buf bytes.Buffer

	err := layer.PackPaths(root, &buf, []string{"usr/bin/tool"})
	if err != nil {
		t.Fatalf("packing a fragment: %v", err)
	}

	into := filepath.Join(t.TempDir(), "fragment")

	err = layer.Unpack(&buf, into)
	if err != nil {
		t.Fatalf("unpacking: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(into, "usr", "bin", "tool"))
	if err != nil {
		t.Fatalf("the path that was asked for did not arrive: %v", err)
	}

	if string(body) != "#!/bin/sh\n" {
		t.Errorf("it arrived as %q", body)
	}

	// The directories above it, with the modes they had.
	for _, dir := range []string{"usr", "usr/bin"} {
		fi, err := os.Stat(filepath.Join(into, dir))
		if err != nil {
			t.Errorf("%s did not arrive: %v", dir, err)

			continue
		}

		if !fi.IsDir() {
			t.Errorf("%s arrived as something other than a directory", dir)
		}
	}

	// And nothing else.
	for _, absent := range []string{"readme", "tool", "var/empty", "usr/bin/same"} {
		_, err := os.Lstat(filepath.Join(into, absent))
		if err == nil {
			t.Errorf("%s arrived, and nobody asked for it"+
				"\n  a fragment that carries the whole layer is a layer", absent)
		}
	}
}

// A fragment is not the layer, and must never be filed as one.
//
// **The boundary the whole idea stands on.** A layer is named by the digest of
// its *whole* tree (§3.2), and a partial materialisation is a materialisation
// strategy rather than a different layer - so it has a different digest, and a
// store that filed it under the layer's name would serve a fragment to every
// later build as though it were the base.
//
// This asserts the difference rather than trusting it, because the failure it
// prevents is silent and permanent.
func TestAFragmentDoesNotHaveTheLayersIdentity(t *testing.T) {
	t.Parallel()

	root := tree(t)

	whole, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	err = layer.PackPaths(root, &buf, []string{"usr/bin/tool"})
	if err != nil {
		t.Fatal(err)
	}

	into := filepath.Join(t.TempDir(), "fragment")

	err = layer.Unpack(&buf, into)
	if err != nil {
		t.Fatal(err)
	}

	got, err := layer.Take(into)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID == whole.ID {
		t.Fatal("a fragment captured as the whole layer's identity" +
			"\n  every later build would take it for the base")
	}
}

// Asking for everything is the whole layer, byte for byte.
//
// The degenerate case has to be exactly `Pack`, or a fragment large enough to
// contain everything would be a *different encoding* of the same tree - two byte
// streams for one layer, which is the determinism problem E262 exists to avoid.
func TestAFragmentOfEverythingIsThePackOfEverything(t *testing.T) {
	t.Parallel()

	root := tree(t)

	var whole, fragment bytes.Buffer

	err := layer.Pack(root, &whole)
	if err != nil {
		t.Fatal(err)
	}

	err = layer.PackPaths(root, &fragment, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(whole.Bytes(), fragment.Bytes()) {
		t.Error("a fragment of everything differs from the whole pack" +
			"\n  two encodings of one tree is the determinism problem again")
	}
}

// A path nobody has is not an error.
//
// The paths come from a *prediction* of what a step will read (I5), and a
// prediction that names something the base does not have is ordinary - the step
// looked for it last time and did not find it. Refusing would turn a hint into a
// requirement.
func TestAPredictedPathThatIsNotThereIsNotAnError(t *testing.T) {
	t.Parallel()

	root := tree(t)

	var buf bytes.Buffer

	err := layer.PackPaths(root, &buf, []string{"usr/bin/tool", "nothing/here"})
	if err != nil {
		t.Fatalf("a predicted path that does not exist was refused: %v", err)
	}

	into := filepath.Join(t.TempDir(), "fragment")
	err = layer.Unpack(&buf, into)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(into, "usr", "bin", "tool"))
	if err != nil {
		t.Errorf("the path that does exist did not arrive: %v", err)
	}
}

// A directory that was asked for brings what is inside it.
//
// The other half of the distinction. A step that read a directory read what was
// in it; sending the directory alone would be the shape of an answer without the
// answer, and the step would fault on every file it then opened - a round trip
// each, which is the cost lazy transfer exists to avoid.
func TestADirectoryAskedForBringsItsContents(t *testing.T) {
	t.Parallel()

	root := tree(t)

	var buf bytes.Buffer

	err := layer.PackPaths(root, &buf, []string{"usr/bin"})
	if err != nil {
		t.Fatal(err)
	}

	into := filepath.Join(t.TempDir(), "fragment")
	err = layer.Unpack(&buf, into)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"usr/bin/tool", "usr/bin/same"} {
		_, err := os.Stat(filepath.Join(into, want))
		if err != nil {
			t.Errorf("%s did not arrive with the directory that was asked for", want)
		}
	}

	// And still nothing outside it.
	_, err = os.Lstat(filepath.Join(into, "readme"))
	if err == nil {
		t.Error("readme arrived, and it is not under usr/bin")
	}
}

// How much of this layer a fragment saves, as a number.
//
// Not an assertion about a figure - the fixture is not a base image - but the
// measurement the decision rests on, computed the way it would be for a real
// one. If a step reads most of what it stands on, lazy transfer buys a round
// trip per file and nothing else.
func TestHowMuchAFragmentSaves(t *testing.T) {
	t.Parallel()

	root := tree(t)

	var whole, part bytes.Buffer

	err := layer.Pack(root, &whole)
	if err != nil {
		t.Fatal(err)
	}

	err = layer.PackPaths(root, &part, []string{"usr/bin/tool"})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("whole %d bytes, one path %d bytes (%.1f%%)",
		whole.Len(), part.Len(), 100*float64(part.Len())/float64(whole.Len()))

	if part.Len() >= whole.Len() {
		t.Errorf("a fragment of one path is %d bytes and the layer is %d",
			part.Len(), whole.Len())
	}
}
