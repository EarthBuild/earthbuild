package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

func TestAMemoAnswersOnlyWhileTheFileIsThere(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	m := OpenExportMemo(root)
	stack := []ir.NodeID{{1}, {2}}
	rel := filepath.Join("layers", "abc", "build", "earthly")

	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	m.Note(stack, "/build/earthly", rel)

	got, ok := m.Lookup(stack, "/build/earthly")
	if !ok || got != rel {
		t.Fatalf("noted %q and got back %q (%v)", rel, got, ok)
	}

	// The layer is collected. The memo still says where it was, and saying so
	// would export a file that is not there - so the stat is what answers.
	if err := os.Remove(filepath.Join(root, rel)); err != nil {
		t.Fatal(err)
	}

	if got, ok := m.Lookup(stack, "/build/earthly"); ok {
		t.Errorf("a memo outlived its file and still answered %q", got)
	}
}

// "a/b" plus "c" and "a" plus "b/c" are different exports, and a key that joined
// them with a separator would say otherwise (green paper 1.4).
func TestTheKeyDistinguishesWhereTheJoinWas(t *testing.T) {
	t.Parallel()

	one := exportMemoKey([]ir.NodeID{{1}, {2}}, "c")
	two := exportMemoKey([]ir.NodeID{{1}}, "\x02c")

	if one == two {
		t.Error("two different exports share a key")
	}

	if exportMemoKey([]ir.NodeID{{1}}, "a") != exportMemoKey([]ir.NodeID{{1}}, "a") {
		t.Error("the same export got two keys")
	}

	if exportMemoKey([]ir.NodeID{{1}, {2}}, "a") == exportMemoKey([]ir.NodeID{{2}, {1}}, "a") {
		t.Error("stack order does not reach the key, so two stacks share one")
	}
}

func TestAMemoWithNoStoreRemembersNothing(t *testing.T) {
	t.Parallel()

	m := OpenExportMemo("")
	m.Note([]ir.NodeID{{1}}, "p", "layers/a/p")

	if _, ok := m.Lookup([]ir.NodeID{{1}}, "p"); ok {
		t.Error("the zero memo answered")
	}
}

// A memo naming somewhere other than the store is refused rather than followed.
func TestAMemoCannotNameSomewhereElse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	m := OpenExportMemo(root)
	stack := []ir.NodeID{{9}}

	for _, rel := range []string{"/etc/passwd", "../../etc/passwd", ""} {
		m.Note(stack, "p", rel)

		if got, ok := m.Lookup(stack, "p"); ok {
			t.Errorf("followed %q to %q", rel, got)
		}
	}
}
