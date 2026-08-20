package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func ctxWith(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for name, body := range files {
		p := filepath.Join(dir, name)
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

const copySrc = versioned + `
build:
    FROM alpine
    COPY src/main.go /app/
    RUN /bin/true
`

// COPY brings host files in, so the *contents* of those files are an input to
// the build.
//
// If identity depended only on the path, editing a source file would leave the
// graph unchanged, every key would still match, and the build would hit the
// cache and produce the previous binary. That is the single most damaging false
// hit a build tool can have, because it looks like a fast build.
func TestEditingACopiedFileChangesTheGraph(t *testing.T) {
	t.Parallel()

	before, err := interp.Build(copySrc, "build",
		interp.WithContext(ctxWith(t, map[string]string{testSourcePath: "package main // one"})))
	if err != nil {
		t.Fatal(err)
	}

	after, err := interp.Build(copySrc, "build",
		interp.WithContext(ctxWith(t, map[string]string{testSourcePath: "package main // two"})))
	if err != nil {
		t.Fatal(err)
	}

	if before.Graph.Root.ID() == after.Graph.Root.ID() {
		t.Error("editing a copied file left the graph identical; the build would hit the cache")
	}
}

// And identical contents produce an identical graph, or nothing ever hits.
func TestIdenticalContextsProduceIdenticalGraphs(t *testing.T) {
	t.Parallel()

	files := map[string]string{testSourcePath: testGoPackage}

	a, err := interp.Build(copySrc, "build", interp.WithContext(ctxWith(t, files)))
	if err != nil {
		t.Fatal(err)
	}

	b, err := interp.Build(copySrc, "build", interp.WithContext(ctxWith(t, files)))
	if err != nil {
		t.Fatal(err)
	}

	if a.Graph.Root.ID() != b.Graph.Root.ID() {
		t.Error("two identical contexts produced different graphs; nothing would ever hit")
	}
}

// COPY adds a step that takes both the previous state and the context.
func TestCopyTakesContextAndPreviousState(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(copySrc, "build",
		interp.WithContext(ctxWith(t, map[string]string{testSourcePath: testGoPackage})))
	if err != nil {
		t.Fatal(err)
	}

	var copyNode *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			copyNode = n
		}
	}

	if copyNode == nil {
		t.Fatal("no COPY node in the graph")
	}

	// One thing it stands on, one thing it reads. The distinction is structural
	// now rather than inferred from an input's kind: a context and an artifact
	// source are both read-from, and only the previous state is stood on.
	if len(copyNode.Inputs) != 1 {
		t.Errorf("COPY stands on %d inputs, want 1 (the state before it)", len(copyNode.Inputs))
	}

	if len(copyNode.Sources) != 1 {
		t.Fatalf("COPY has %d sources, want 1 (the build context)", len(copyNode.Sources))
	}

	if got := copyNode.Sources[0].Op.Kind; got != ir.OpLocal {
		t.Errorf("COPY's source is %v, want the local context", got)
	}
}

// A COPY naming something absent must fail at parse time, not halfway through a
// build - and must say what it was looking for and where.
func TestCopyOfAMissingPathIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(copySrc, "build", interp.WithContext(ctxWith(t, map[string]string{"other": "x"})))
	if err == nil {
		t.Fatal("COPY of a missing path was accepted")
	}

	for _, want := range []string{testSourcePath, "Earthfile:5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// Without a context, COPY cannot be resolved, and guessing an empty one would
// silently build an image missing the application.
func TestCopyWithoutAContextIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(copySrc, "build")
	if err == nil {
		t.Fatal("COPY with no build context was accepted")
	}

	if !strings.Contains(err.Error(), "context") {
		t.Errorf("refusal does not mention the missing context:\n%s", err)
	}
}

// A relative build context must work: `earth-native --dir .` is the ordinary
// invocation, and `.` joined with a source path does not have the root as a
// textual prefix.
//
// Comparing a joined path against an unnormalised root is the same defect that
// made the unpacker refuse every entry on macOS. Normalise both ends, or
// neither comparison means anything.
func TestRelativeContextsAreResolved(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{testSourcePath: testGoPackage})

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// A relative path to the context, rather than chdir'ing into it and passing
	// ".". The property is that a relative context resolves; the chdir was only
	// a way to produce one, and it is process-global - so with the package's
	// tests running in parallel it moved the working directory out from under
	// every other test, which then could not find files they name relatively:
	//
	//	deterministic_test.go:42: open ../../Earthfile: no such file or directory
	//
	// The linter that asks for t.Parallel is what surfaced it, which is the
	// argument for the linter: the hazard was there the whole time and only a
	// second test running at the same moment could show it.
	rel, err := filepath.Rel(wd, dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = interp.Build(copySrc, "build", interp.WithContext(rel))
	if err != nil {
		t.Errorf("a relative build context was refused: %v", err)
	}
}
