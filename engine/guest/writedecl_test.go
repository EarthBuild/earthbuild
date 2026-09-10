package guest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What a stack element declares is handed over exactly as it lies.
//
// **Byte-for-byte, not re-encoded.** The host decodes it with `decl.Decode`,
// which is the same reader the store uses, so anything that round-tripped the
// value through a struct on the way out would be a second encoder to disagree
// with the first.
func TestADeclarationIsHandedOverAsItLies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	want := decl.Declaration{
		Env:        []string{"PATH=/usr/local/cargo/bin:/usr/bin", "CARGO_HOME=/usr/local/cargo"},
		WorkingDir: "/w",
	}

	id, err := decl.Write(root, want)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer

	n, held, err := WriteDeclaration(root, id, &out)
	if err != nil {
		t.Fatalf("hand over: %v", err)
	}

	if !held {
		t.Fatal("a declaration the store holds was reported absent")
	}

	if n != int64(out.Len()) {
		t.Errorf("counted %d, wrote %d", n, out.Len())
	}

	// The count is what a transport reads back, so it has to be the file's, and
	// the bytes have to decode to what went in.
	onDisk, err := os.ReadFile(filepath.Join(root, "layers", id.String()+".decl"))
	if err == nil && !bytes.Equal(onDisk, out.Bytes()) {
		t.Error("the bytes handed over are not the bytes in the store")
	}

	got, err := decl.Decode(out.Bytes())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.WorkingDir != want.WorkingDir || len(got.Env) != len(want.Env) {
		t.Errorf("got %+v, wanted %+v", got, want)
	}
}

// A stack element that is a tree declares nothing, and that is an answer rather
// than a failure: asking about every element is how the caller finds the few
// that declare something.
func TestAnElementThatDeclaresNothingIsNotAnError(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	n, held, err := WriteDeclaration(t.TempDir(), ir.NodeID{1}, &out)
	if err != nil {
		t.Fatalf("hand over: %v", err)
	}

	if held || n != 0 || out.Len() != 0 {
		t.Errorf("an absent declaration reported held=%v n=%d bytes=%d", held, n, out.Len())
	}
}
