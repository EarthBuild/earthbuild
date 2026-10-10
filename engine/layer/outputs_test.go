package layer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// An action's declared outputs are named one by one.
//
// **A client asks for paths and is answered about paths.** REAPI's ActionResult
// carries an entry per declared output; this engine captures a filesystem and
// was handing back the whole of it as a single directory with no name. Buck2
// accepts that result, tries to extract the artifacts it asked for, and fails
// with "Path is empty" - it has what it wanted and no way to tell which part
// of it is which.
//
// Derived from the manifest rather than recorded while capturing, because the
// same answer has to come out of a cache hit, where nothing was captured and
// the manifest beside the layer is all there is.
func TestDeclaredOutputsAreNamedOneByOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"top.txt":         "the top file",
		"run.sh":          "#!/bin/sh\n",
		"out/a.txt":       "first",
		"out/deep/b.txt":  "second",
		"untouched/c.txt": "not asked for",
	})

	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := layer.Outputs(m, []string{"top.txt", "run.sh", "out", "never-made"})
	if err != nil {
		t.Fatal(err)
	}

	// Files, by name, with the contents they actually have.
	files := map[string]layer.OutputFile{}
	for _, f := range got.Files {
		files[f.Path] = f
	}

	if len(files) != 2 {
		t.Errorf("named %d files, and two were declared: %v", len(files), got.Files)
	}

	if f := files["top.txt"]; f.Size != int64(len("the top file")) {
		t.Errorf("top.txt is %d bytes, and it holds %d", f.Size, len("the top file"))
	}

	// **The one mode bit REAPI carries.** A client materialises what it is told,
	// and a script that arrives without it cannot be run.
	if !files["run.sh"].Executable {
		t.Error("run.sh was declared executable and is named as an ordinary file")
	}

	if files["top.txt"].Executable {
		t.Error("top.txt is not executable and is named as though it were")
	}

	// The directory, named once - not as the files beneath it.
	if len(got.Dirs) != 1 || got.Dirs[0].Path != "out" {
		t.Fatalf("named %v, and one directory was declared", got.Dirs)
	}

	// Both digests, because peers differ about which they read.
	if got.Dirs[0].Root == (ir.NodeID{}) || got.Dirs[0].Tree == (ir.NodeID{}) {
		t.Errorf("out is named with root=%v tree=%v, and a client reads one or"+
			" the other", got.Dirs[0].Root, got.Dirs[0].Tree)
	}

	// **What was not produced is not named**, which is REAPI's rule and the
	// honest one: an entry for it would claim an artefact that is not there.
	for _, f := range got.Files {
		if f.Path == "never-made" {
			t.Error("a declared output the action did not produce was named anyway")
		}
	}

	// And nothing undeclared leaks in.
	for _, f := range got.Files {
		if f.Path == "untouched/c.txt" {
			t.Error("a file nobody declared was named")
		}
	}
}

// A step that declared nothing is named as it always was.
//
// Every ordinary RUN is this: its output is the filesystem it produced, and
// there is no list to enumerate.
func TestNoDeclaredOutputsNamesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.txt": "x"})

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := layer.Outputs(m, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Files) != 0 || len(got.Dirs) != 0 {
		t.Errorf("a step declaring nothing was given %d files and %d directories",
			len(got.Files), len(got.Dirs))
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for p, content := range files {
		at := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(at, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
