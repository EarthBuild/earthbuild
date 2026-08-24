package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/distribution/reference"
)

// No value the engine derived from an Earthfile may begin with a dash.
//
// This is the general form of a defect found three times in one day, each time
// by accident and each time in a different command: RUN, IF and SAVE ARTIFACT
// all shipped with their options unparsed, so the flag became the first word of
// a command, a condition, or an artifact path. The shape they share is that
// syntax the engine was supposed to *consume* survived into a value.
//
// A leading dash is the signal. `RUN ls --color` is ordinary and this does not
// object to it; a *command* that begins with `--`, an artifact path that begins
// with `--`, an image reference or a working directory that does, are all
// nonsense that only an unparsed flag produces.
//
// Run over the whole corpus rather than a table of cases, because the three
// instances were found in three separate hand-written checks and the fourth
// would have been too.
func TestNoConsumedSyntaxSurvivesIntoAValue(t *testing.T) {
	t.Parallel()

	// Skipped under -short, which is how the race-instrumented run stays
	// usable: these walk every Earthfile in the repository, and instrumentation
	// multiplies that by about ten. They run in full on every ordinary pass.
	if testing.Short() {
		t.Skip("corpus sweep")
	}

	files := corpus(t)

	var checked int

	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		for _, target := range targetsIn(string(src)) {
			p, err := interp.Build(string(src), target, interp.WithContext(filepath.Dir(f)))
			if err != nil {
				// A refusal is not this test's business: it is about what
				// survives into a plan that was accepted.
				continue
			}

			checked++

			where := f + " [" + target + "]"

			for _, n := range p.Graph.Nodes() {
				for _, bad := range leadingDashes(n) {
					t.Errorf("%s: %s", where, bad)
				}
			}

			for _, a := range p.Artifacts {
				if dashed(a.Path) {
					t.Errorf("%s: artifact path is %q (%s)", where, a.Path, a.Source)
				}

				if dashed(a.LocalDest) {
					t.Errorf("%s: artifact destination is %q (%s)", where, a.LocalDest, a.Source)
				}
			}

			for _, img := range p.Images {
				if dashed(img.Ref) {
					t.Errorf("%s: image reference is %q", where, img.Ref)
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("no target planned, so this checked nothing")
	}

	t.Logf("checked %d planned targets across %d Earthfiles", checked, len(files))
}

// leadingDashes reports the values of a node that begin with a dash.
func leadingDashes(n *ir.Node) []string {
	var out []string

	if dashed(n.Op.Dir) {
		out = append(out, "working directory is "+n.Op.Dir+" ("+n.Meta.Source+")")
	}

	if dashed(n.Op.User) {
		out = append(out, "user is "+n.Op.User+" ("+n.Meta.Source+")")
	}

	// Partial on purpose: only the kinds that carry a command have one to read.
	switch n.Op.Kind { //nolint:exhaustive // partial on purpose, see above
	case ir.OpExec, ir.OpHost:
		// Shell form: the command is the string after `-c`, and a command that
		// starts with a dash is a flag that was never read.
		cmd := ""

		if len(n.Op.Args) == 3 && n.Op.Args[0] == testShell && n.Op.Args[1] == "-c" {
			cmd = n.Op.Args[2]
		} else if len(n.Op.Args) > 0 {
			cmd = n.Op.Args[0]
		}

		if dashed(cmd) {
			out = append(out, "command begins with a flag: "+cmd+" ("+n.Meta.Source+")")
		}

	case ir.OpImage, ir.OpLocal, ir.OpFile, ir.OpBuild, ir.OpMerge:
		for _, a := range n.Op.Args {
			if dashed(a) {
				out = append(out, n.Op.Kind.String()+" argument is "+a+" ("+n.Meta.Source+")")
			}
		}
	}

	return out
}

func dashed(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "--") }

// Every image reference a plan names must actually be a reference.
//
// The dash sweep found `FROM --platform=...` becoming an image name by asking
// whether a value looked wrong. This asks the stronger question - whether the
// value is one the registry code can use - and it is the same question the
// build will ask later, only asked while there is still a line number to blame.
// An unexpanded `$TAG`, a quote the lexer left behind and a flag read as a name
// all fail it, and none of them needs a rule of its own.
func TestEveryImageReferenceParses(t *testing.T) {
	t.Parallel()

	// Skipped under -short, which is how the race-instrumented run stays
	// usable: these walk every Earthfile in the repository, and instrumentation
	// multiplies that by about ten. They run in full on every ordinary pass.
	if testing.Short() {
		t.Skip("corpus sweep")
	}

	var checked int

	for _, f := range corpus(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		for _, target := range targetsIn(string(src)) {
			p, err := interp.Build(string(src), target, interp.WithContext(filepath.Dir(f)))
			if err != nil {
				continue
			}

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind != ir.OpImage || len(n.Op.Args) == 0 {
					continue
				}

				checked++

				_, err := reference.ParseNormalizedNamed(strings.TrimSpace(n.Op.Args[0]))
				if err != nil {
					t.Errorf("%s [%s]: %q is not an image reference (%s): %v",
						f, target, n.Op.Args[0], n.Meta.Source, err)
				}
			}

			for _, img := range p.Images {
				_, err := reference.ParseNormalizedNamed(strings.TrimSpace(img.Ref))
				if err != nil {
					t.Errorf("%s [%s]: SAVE IMAGE %q is not a reference: %v", f, target, img.Ref, err)
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("no image reference checked")
	}

	t.Logf("checked %d image references", checked)
}

// An artifact must be produced by a step that is in the graph.
//
// If it is not, the failure arrives at export time as "the step producing X did
// not run", which describes a symptom and names no line to fix. The graph
// already knows, so it can be asked now.
func TestEveryArtifactIsProducedByTheGraph(t *testing.T) {
	t.Parallel()

	// Skipped under -short, which is how the race-instrumented run stays
	// usable: these walk every Earthfile in the repository, and instrumentation
	// multiplies that by about ten. They run in full on every ordinary pass.
	if testing.Short() {
		t.Skip("corpus sweep")
	}

	var checked int

	for _, f := range corpus(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		for _, target := range targetsIn(string(src)) {
			p, err := interp.Build(string(src), target, interp.WithContext(filepath.Dir(f)))
			if err != nil {
				continue
			}

			in := map[ir.NodeID]bool{}
			for _, n := range p.Graph.Nodes() {
				in[n.ID()] = true
			}

			for _, a := range p.Artifacts {
				checked++

				if a.From == nil {
					t.Errorf("%s [%s]: artifact %q has no producing step (%s)", f, target, a.Path, a.Source)

					continue
				}

				if !in[a.From.ID()] {
					t.Errorf("%s [%s]: artifact %q is produced by a step outside the graph (%s)",
						f, target, a.Path, a.Source)
				}
			}
		}
	}

	t.Logf("checked %d artifacts", checked)
}
