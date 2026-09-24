package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// A diagnostic names a command the way the parser spells it.
//
// The parser holds the language's own list - `earthfile.CmdSaveImage` and its
// twenty-nine siblings - and every command the interpreter reports on has an
// entry there. Where the interpreter spells one out again as a literal, the two
// are equal by coincidence rather than by construction, and renaming the
// constant leaves a message naming a command that no longer exists.
//
// The failure would be quiet: the parser would accept the new spelling, the
// build would work, and only the error text of a *broken* build would be wrong -
// which is the one place a reader has nothing else to go on.
//
// Measured by drifting the literal in `interp.go` to `SAVE IMG`: red. With the
// name taken from the constant: green. The first attempt mutated the *constant*
// instead, which is not the same experiment - the constant **is** the keyword
// the lexer matches, so renaming it stops `SAVE IMAGE` parsing at all and the
// build fails with `not supported by the native engine`, a message that could
// never have named the command whatever the interpreter did (E200).
//
// Asserted through a real parse error rather than by comparing the constant to
// itself, because the question is what a person is told, not what a package
// declares.
func TestADiagnosticNamesACommandAsTheParserSpellsIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		source string
		cmd    earthfile.Cmd
	}{
		{
			name: "save image",
			source: versioned + `
build:
    FROM alpine:3.22
    SAVE IMAGE --no-such-flag myorg/tool:latest
`,
			cmd: earthfile.CmdSaveImage,
		},
		{
			name: "save artifact",
			source: versioned + `
build:
    FROM alpine:3.22
    SAVE ARTIFACT --no-such-flag out.txt
`,
			cmd: earthfile.CmdSaveArtifact,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(tc.source, "build")
			if err == nil {
				t.Fatal("an unknown flag was accepted, so there is no" +
					" diagnostic to check")
			}

			if !strings.Contains(err.Error(), string(tc.cmd)) {
				t.Errorf("the diagnostic does not name the command as the"+
					" parser spells it\n  parser %q\n  said   %v",
					string(tc.cmd), err)
			}
		})
	}
}
