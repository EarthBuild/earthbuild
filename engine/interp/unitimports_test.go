package interp

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// TestAUnitKnowsItsOwnImportsBeforeAnythingRunsIt.
//
// **An IMPORT is a declaration about the file, not a step in it.** The map was
// filled only when the `IMPORT` command was *interpreted*, which happens while
// walking a unit's base recipe - and a unit entered at one of its functions is
// never walked that way. So a function calling through an alias its own file
// declared was told the alias "was never imported":
//
//	VERSION 0.6
//	IMPORT github.com/earthly/hello-world:main
//
//	FROM_HELLO_WORLD:
//	  COMMAND
//	  FROM hello-world+hello
//
// which is `earthly-command-example/import/Earthfile`, reached from
// `tests/import.earth+test-command-import`. References resolve against the
// defining file, so the defining file has to know its own imports the moment it
// is loaded.
func TestAUnitKnowsItsOwnImportsBeforeAnythingRunsIt(t *testing.T) {
	t.Parallel()

	tree, err := earthfile.Parse("Earthfile", `VERSION 0.6

IMPORT github.com/earthly/hello-world:main
IMPORT ./local/dir AS mine
IMPORT --allow-privileged github.com/org/priv:main AS trusted

FROM_HELLO_WORLD:
  COMMAND
  FROM hello-world+hello
`, earthfile.WithSourceMap())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	u, err := newUnit(tree, "/somewhere", nil)
	if err != nil {
		t.Fatalf("newUnit: %v", err)
	}

	for _, c := range []struct{ alias, want string }{
		// The default name is the last path component, with any tag removed.
		{"hello-world", "github.com/earthly/hello-world:main"},
		{"mine", "./local/dir"},
		{"trusted", "github.com/org/priv:main"},
	} {
		if got := u.imports[c.alias]; got != c.want {
			t.Errorf("imports[%q] = %q, want %q", c.alias, got, c.want)
		}
	}

	// And the grant travels with the name, as it does when interpreted.
	if !u.grants["trusted"] {
		t.Error("`IMPORT --allow-privileged ... AS trusted` did not grant the alias")
	}

	if u.grants["mine"] {
		t.Error("an ordinary IMPORT granted privilege")
	}
}
