package cli

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Every output a plan declares has something that reads it.
//
// Twice in two iterations a lower layer declared an output and the layer above
// never read it: the artifact a FINALLY saved, and the image a SAVE IMAGE
// named. Both built correctly, both were dropped on the floor, and both
// reported success - because a unit test is written per component and a seam
// belongs to nobody.
//
// So the seam gets a guard of its own, in the shape the key-coverage test
// already established: a field added to interp.Plan and not listed here fails,
// and listing it is a statement about where it is acted on. The compiler cannot
// notice an ignored field; this can.
func TestEveryPlanOutputIsConsumed(t *testing.T) {
	t.Parallel()

	consumedBy := map[string]string{
		"Graph":     "build: scheduled",
		"Artifacts": "exportAll: written to the project directory",
		"Images":    "writeImages: written as an OCI layout under the build cache",
		"Pinned":    "recordPinning: printed as provenance, and the digests are already in the graph",
		"PinCost":   "recordPinning: quoted in the note, so the advice carries what it is worth",
	}

	plan := reflect.TypeFor[interp.Plan]()

	// The claim is that every output is consumed; with no outputs it is true and
	// worthless. Reflection makes the empty case a live possibility rather than
	// a hypothetical - a renamed type answers zero fields, not an error.
	if plan.NumField() == 0 {
		t.Fatal("the plan type reports no fields at all, so this checks nothing")
	}

	for f := range plan.Fields() {
		if !f.IsExported() {
			continue
		}

		where, ok := consumedBy[f.Name]
		if !ok {
			t.Errorf("interp.Plan.%s is declared and nothing here reads it.\n"+
				"  Either act on it, or add it to consumedBy saying what does.\n"+
				"  A declared output that no one consumes is a build reporting success "+
				"for work it did not do.", f.Name)

			continue
		}

		if where == "" {
			t.Errorf("interp.Plan.%s is listed with no explanation", f.Name)
		}
	}

	// And the other way: a name listed here that no longer exists is a note
	// about a field somebody removed, which will mislead the next reader.
	for name := range consumedBy {
		if _, ok := plan.FieldByName(name); !ok {
			t.Errorf("consumedBy names %q, which interp.Plan no longer has", name)
		}
	}
}

// The same question, one level down.
//
// A Plan's outputs are consumed, and each of those carries fields of its own -
// so the gap simply moves inward: `Image.Push` is recorded by the interpreter
// and read by nothing. That one is *right*, and the difference is the point of
// this test. Pushing happens when the invocation asks for it, which is how the
// tool that ships behaves, and no invocation flag exists yet - so recording the
// declaration and not acting on it is correct.
//
// What is not acceptable is that being indistinguishable from an oversight. A
// field listed here says someone decided; a field missing says nobody has.
func TestEveryOutputFieldIsAccountedFor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		what  reflect.Type
		known map[string]string
	}{
		{
			what: reflect.TypeFor[interp.Image](),
			known: map[string]string{
				"Ref":    "writeImages: the layout's ref name",
				"Config": "specFor: entrypoint, environment, labels",
				"From":   "writeImages: the step whose layers make the image",
				"Source": "writeImages: named in diagnostics",
				"Push": "declared and deliberately not acted on - pushing happens when the " +
					"invocation asks, as it does in the tool that ships, and no invocation " +
					"flag exists yet. A build that does not push has been told to push nothing.",
			},
		},
		{
			what: reflect.TypeFor[interp.Artifact](),
			known: map[string]string{
				"Path":      "exportAll: what to take out of the filesystem",
				"LocalDest": "exportAll: where it lands, empty meaning it is not exported",
				"From":      "exportAll: the step that produced it",
				"Source":    "exportAll: named in diagnostics",
				"IfExists":  "Export: an absent path is not a failure",
				"Name": "consumed inside the interpreter rather than here: `COPY +build/*` " +
					"lands each artifact under the name it was given, because a pattern's " +
					"match carries a version the author did not write. Nothing the CLI does " +
					"needs it, and exporting under it would rename what `AS LOCAL` already " +
					"placed.",
			},
		},
	} {
		t.Run(tc.what.Name(), func(t *testing.T) {
			t.Parallel()

			for f := range tc.what.Fields() {
				if !f.IsExported() {
					continue
				}

				if _, ok := tc.known[f.Name]; !ok {
					t.Errorf("%s.%s is declared and nothing accounts for it.\n"+
						"  Either act on it, or record here why not acting on it is right.",
						tc.what.Name(), f.Name)
				}
			}

			for name := range tc.known {
				if _, ok := tc.what.FieldByName(name); !ok {
					t.Errorf("%s no longer has a field called %q", tc.what.Name(), name)
				}
			}
		})
	}
}
