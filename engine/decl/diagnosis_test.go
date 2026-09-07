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

// Damage in the store says where the file is and what to do about it.
//
// A declaration is named by its contents, so the remedy is unusually simple and
// unusually easy to miss: delete it and it will be fetched again. A message that
// stops at "damaged" leaves the reader wondering whether they have lost
// something.
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

	for _, want := range []string{decl.Path(store, id), "delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the damage report does not mention %q:\n  %v", want, err)
		}
	}
}
