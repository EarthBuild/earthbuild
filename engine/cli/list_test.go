package cli_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// `ls` names every target the file offers, sorted, one per line.
//
// `tests/Earthfile` asserts the whole output against a fixed list:
//
//	RUN earthly ls 2>/dev/null | tee actual
//	RUN echo -e "+alpha\n+base\n+bravo\n+charlie" > expected
//	RUN diff expected actual
//
// which fixes three things at once - the `+` prefix, the sort, and that the base
// recipe is named too even though `tests/ls.earth` declares no target called
// `base` (E474).
func TestListNamesEveryTargetSorted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	src, err := os.ReadFile("../../tests/ls.earth")
	if err != nil {
		// See docOf: `tests/` is not copied into `+unit-test`'s context, so
		// absent is "not here" rather than "wrong" (E605).
		if errors.Is(err, fs.ErrNotExist) {
			t.Skip("tests/ls.earth is not in this copy of the repository")
		}

		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "Earthfile"), src, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	err = cli.List(cli.Options{Dir: dir, Out: &out})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}

	const want = "+alpha\n+base\n+bravo\n+charlie\n"

	if got := out.String(); got != want {
		t.Errorf("ls printed\n%s\nand the tree diffs it against\n%s", got, want)
	}
}

// A file whose targets are already in order is still printed in order.
//
// Sorted rather than as-written: `tests/ls.earth` declares alpha, charlie,
// bravo, so the two orders differ and the tree's expected output picks the
// sorted one. Asserted from the other side as well, because a sort that happens
// to agree with the source order proves nothing about the sort.
func TestListIsSortedRatherThanAsWritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"),
		[]byte("VERSION 0.8\n\nzulu:\n    RUN echo z\n\nalpha:\n    RUN echo a\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	err = cli.List(cli.Options{Dir: dir, Out: &out})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}

	// `+base` is here although this file has no base recipe at all, and that is
	// the reading rather than a witnessed fact: the corpus has one example and
	// it *does* have one, so *a rule read off one example is a rule about one
	// example*. `+base` is askable either way - an empty base recipe is a target
	// that builds nothing - so `ls` naming it answers the question the command
	// asks, which is what can be asked for here.
	if got, want := out.String(), "+alpha\n+base\n+zulu\n"; got != want {
		t.Errorf("ls printed %q, and %q is the sorted answer", got, want)
	}
}

// A file with no Earthfile says so, and says where it looked.
func TestListSaysWhereItLooked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := cli.List(cli.Options{Dir: dir, Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("a directory with no Earthfile listed targets from nowhere")
	}

	if !strings.Contains(err.Error(), dir) {
		t.Errorf("refused with %q, which does not say where it looked", err)
	}
}

// A reading command answers a file it could never build.
//
// `ls` and `doc` start no sandbox, plan nothing and run nothing - which is easy
// to say and easy to lose, because every other entry point in this package
// starts a machine. Asserted structurally rather than by a clock: the Earthfile
// here needs a repository that cannot be fetched and a daemon that is not
// running, so anything that planned it would fail, and anything that built it
// would take a VM to find out (E477).
//
// *A timing threshold measures the machine* (E473), and "it was quick" is not
// the property anyway. The property is that the answer comes from the file.
func TestAReadingCommandNeedsNothingButTheFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"), []byte(
		"VERSION 0.8\n"+
			"\n# unbuildable documents a target nothing here can build.\n"+
			"unbuildable:\n"+
			"    FROM github.com/nobody/nothing:main+base\n"+
			"    WITH DOCKER --pull alpine:3.22\n        RUN docker images\n    END\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	err = cli.List(cli.Options{Dir: dir, Out: &out})
	if err != nil {
		t.Fatalf("ls needed more than the file: %v", err)
	}

	if got := out.String(); !strings.Contains(got, "+unbuildable") {
		t.Errorf("ls printed %q", got)
	}

	out.Reset()

	err = cli.Doc(cli.Options{Dir: dir, Out: &out, Long: true})
	if err != nil {
		t.Fatalf("doc needed more than the file: %v", err)
	}

	if got := out.String(); !strings.Contains(got, "unbuildable documents a target") {
		t.Errorf("doc printed %q", got)
	}
}
