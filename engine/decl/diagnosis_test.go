package decl_test

import (
	"os"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
)

// A refusal says what was expected, what was found, and what that is.
//
// "not a declaration" is true and useless. The reader has a byte stream that
// came from somewhere, and what they need is which stream they actually have -
// a layer pack passed where a declaration was expected is a wiring mistake with
// an obvious fix, and it reads identically to corruption unless the message
// says so.
func TestARefusalNamesWhatItFound(t *testing.T) {
	t.Parallel()

	_, err := decl.Decode([]byte("EBLAYER1and then some bytes"))
	if err == nil {
		t.Fatal("a layer pack decoded as a declaration")
	}

	for _, want := range []string{"EBDECL1", "EBLAYER1", "layer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n  %v", want, err)
		}
	}
}

// A truncation says which field ran out and where.
//
// A length and a remainder are enough to know something is short; they are not
// enough to know *what* is short. The field name is what turns "this file is
// damaged" into "this file is damaged after Env", which is where somebody looks.
func TestATruncationNamesTheFieldAndTheOffset(t *testing.T) {
	t.Parallel()

	whole := decl.Encode(full())

	_, err := decl.Decode(whole[:len(whole)-4])
	if err == nil {
		t.Fatal("a truncated declaration decoded")
	}

	for _, want := range []string{"offset", "Cmd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the truncation does not mention %q:\n  %v", want, err)
		}
	}
}

// Damage in the store says where the file was and what has been done about it.
//
// A declaration is named by its contents, so the remedy is unusually simple -
// and the engine now applies it rather than describing it: the file is removed
// and will be fetched again. The message has to say so, because a build that
// fails and then works without anybody touching anything is otherwise
// indistinguishable from a flake.
//
// It said "safe to delete" until the removal was automatic. That wording is what
// this asserted, and updating it is the behaviour changing rather than the test
// being loosened: what is still required is the path, and what became of it.
func TestDamageSaysWhereAndWhatToDo(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	id, err := decl.Write(store, full())
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	err = os.WriteFile(decl.Path(store, id), []byte("rubbish"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = decl.Read(store, id)
	if err == nil {
		t.Fatal("a damaged declaration read back clean")
	}

	for _, want := range []string{decl.Path(store, id), "removed", "fetched again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the damage report does not mention %q:\n  %v", want, err)
		}
	}
}
