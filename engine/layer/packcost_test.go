package layer_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Packing part of a layer does not read the whole layer.
//
// **The other half of E337.** Serving one file of a layer hashed every file's
// contents: the manifest is memoised now, and the pack still walks. A fragment
// of a 400-file layer cost twenty times a fragment of a 20-file one, for the
// same one file, and that ratio is what makes lazy transfer scale with the wrong
// number.
//
// The manifest genuinely needs every path - it describes them - but it needs
// each file's digest, which is what `walk` computes. A **pack** needs the
// contents of what it is sending and the metadata of what it is scaffolding, and
// nothing else (E338).
//
// Measured by scale rather than by a threshold: a machine's absolute speed is
// not the property, the shape of the curve is.
func TestPackingPartOfALayerDoesNotReadTheWholeLayer(t *testing.T) {
	// Not parallel: it counts work done by the whole package.
	root := treeOf(t, 400)

	one := []string{"usr/lib/lib0.so"}

	var buf bytes.Buffer

	before := layer.DigestedForTest()

	err := layer.PackPaths(root, &buf, one)
	if err != nil {
		t.Fatalf("%v", err)
	}

	read := layer.DigestedForTest() - before

	t.Logf("packing one file of a 400-file layer read %d file(s)", read)

	// One file, and no more than one: the walk still stats every path, because
	// a pack carries the directories its files live in and cannot know which
	// those are without looking. What it must not do is *read* them.
	if read > 1 {
		t.Errorf("packing one file of a 400-file layer read %d files"+
			"\n  a fragment's price should be its own size, not its layer's"+
			" (E338, E350)", read)
	}

	// And a whole pack still reads everything, or the flag would be a way of
	// producing a layer that is missing its contents.
	before = layer.DigestedForTest()

	buf.Reset()

	err = layer.Pack(root, &buf)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if read = layer.DigestedForTest() - before; read < 400 {
		t.Errorf("packing a whole 400-file layer read %d files", read)
	}
}

// treeOf is a directory of n files, each big enough that hashing it is work.
func treeOf(t *testing.T, n int) string {
	t.Helper()

	return treeOfSized(t, n, 8192*8)
}

// treeOfSized is a directory of n files of a stated size.
func treeOfSized(t *testing.T, n, each int) string {
	t.Helper()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "usr", "lib"), 0o750)
	if err != nil {
		t.Fatalf("%v", err)
	}

	for i := range n {
		body := bytes.Repeat([]byte(fmt.Sprintf("%08d", i)), each/8)

		err := os.WriteFile(
			filepath.Join(root, "usr", "lib", fmt.Sprintf("lib%d.so", i)),
			body, 0o600)
		if err != nil {
			t.Fatalf("%v", err)
		}
	}

	return root
}

// A fragment carries the bytes it says it carries.
//
// **Nothing checked this.** Mutation removed the pass that reads the files a
// fragment does send and no test noticed - so a fragment could go out with every
// body filed under the same empty digest, which is not a slow fragment or a
// refused one but a wrong one.
//
// The round trip is the check: pack part of a tree, unpack it elsewhere, and ask
// the layer's own manifest whether what arrived is what the layer says is there
// (I13). That is exactly what a worker does with a fragment, and it is the path
// every lazy build takes (E338).
func TestAFragmentCarriesTheBytesItSaysItCarries(t *testing.T) {
	t.Parallel()

	root := treeOf(t, 8)

	want := []string{"usr/lib/lib3.so", "usr/lib/lib5.so"}

	manifest, err := layer.Manifest(root)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var packed bytes.Buffer

	if err = layer.PackPaths(root, &packed, want); err != nil {
		t.Fatalf("%v", err)
	}

	into := t.TempDir()

	if err = layer.Unpack(&packed, into); err != nil {
		t.Fatalf("%v", err)
	}

	if err = layer.VerifyFragment(manifest, into); err != nil {
		t.Fatalf("a fragment of a layer did not check out against it: %v", err)
	}

	for _, p := range want {
		got, readErr := os.ReadFile(filepath.Join(into, filepath.FromSlash(p)))
		if readErr != nil {
			t.Fatalf("%v", readErr)
		}

		was, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if readErr != nil {
			t.Fatalf("%v", readErr)
		}

		if !bytes.Equal(got, was) {
			t.Errorf("%s arrived with %d bytes, want %d", p, len(got), len(was))
		}
	}

	// And nothing else came with them: a fragment that quietly carried the
	// whole layer would pass every check above and cost what E338 removed.
	if _, err = os.Stat(filepath.Join(into, "usr", "lib", "lib0.so")); err == nil {
		t.Error("a fragment of two files carried a third")
	}
}

// What a fragment actually weighs, proof and all.
//
// **The next question, asked before anything is built for it.** A fragment of a
// large base is a handful of files; the manifest that authenticates it (I13,
// C.4.1) describes *every* path in the layer. Which of the two dominates decides
// what is worth doing next, and the last three experiments were spent on
// plausible causes that were not the cause (E335, E337).
func TestWhatAFragmentWeighs(t *testing.T) {
	t.Parallel()

	// **The shape lazy transfer exists for**: a large base of many modest files,
	// of which a step reads a handful. A corpus of a few enormous files gives
	// the opposite answer and is not the case anybody has.
	root := treeOfSized(t, 2000, 8192)

	want := make([]string, 0, 10)
	for i := range 10 {
		want = append(want, fmt.Sprintf("usr/lib/lib%d.so", i))
	}

	manifest, err := layer.Manifest(root)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var packed bytes.Buffer

	if err = layer.PackPaths(root, &packed, want); err != nil {
		t.Fatalf("%v", err)
	}

	var whole bytes.Buffer

	if err = layer.Pack(root, &whole); err != nil {
		t.Fatalf("%v", err)
	}

	t.Logf("a 2000-file layer is %d bytes; ten files of it pack to %d, and the"+
		" proof that they belong is %d",
		whole.Len(), packed.Len(), len(manifest))

	// Not a threshold on either number - they are properties of a corpus, not
	// of the engine. What is asserted is the thing a design decision would rest
	// on: whether the proof is the dominant part of a small fragment.
	if len(manifest) < packed.Len() {
		t.Logf("the proof is smaller than the fragment, so the fragment is" +
			" what to shrink")
	} else {
		t.Logf("the proof is %.1fx the fragment, so the proof is what to shrink",
			float64(len(manifest))/float64(packed.Len()))
	}
}
