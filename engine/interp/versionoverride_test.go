package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A feature flag can be turned on from outside the file.
//
// `--version-flag-overrides` is how the corpus drives files whose dialect it
// wants to vary without editing them: seven of `tests/Earthfile`'s invocations
// pass it, and until now the run gate could not attempt any of them because the
// engine had nowhere to put the answer (E473).
//
// The flag names features exactly as a VERSION line does, without the dashes.
func TestAVersionFlagCanBeSuppliedByTheCaller(t *testing.T) {
	t.Parallel()

	// `SET` needs `--arg-scope-and-set`, which 0.8 does not turn on by itself -
	// so the same source is a refusal without the override and a build with it,
	// which is the whole of what the flag is for.
	//
	// Not `COMMAND`: the first version of this used it, and 0.8 already has the
	// new keyword, so the file was refused before the override could decide
	// anything. A gate that is already closed proves nothing about the key.
	src := versioned + "\nFROM alpine:3.22\nARG --global x=1\n" +
		"\nmain:\n    SET x = 2\n    RUN echo $x\n"

	_, err := interp.Build(src, testMain)
	if err == nil {
		t.Fatal("SET without its feature was accepted, so the flag gates nothing")
	}

	if _, err := interp.Build(src, testMain,
		interp.WithVersionFlags([]string{"arg-scope-and-set"})); err != nil {
		t.Fatalf("the caller turned the feature on and the file was still refused: %v", err)
	}
}

// A flag the engine does not know is refused, and named.
//
// The same rule the VERSION line itself has: an override that reaches nothing is
// a caller who asked for a dialect and got another one silently.
func TestAnUnknownVersionOverrideIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nmain:\n    FROM alpine:3.22\n    RUN echo hi\n",
		testMain, interp.WithVersionFlags([]string{"no-such-feature"}))
	if err == nil {
		t.Fatal("an override naming nothing was accepted, so the dialect asked for is not the one built")
	}

	if !strings.Contains(err.Error(), "no-such-feature") {
		t.Errorf("refused with %q, which does not name the flag", err)
	}
}

// A flag written with its dashes means the same thing.
//
// The corpus writes `--version-flag-overrides=require-force-for-unsafe-saves`
// without them; a caller who writes them is naming the same feature and should
// not be told it does not exist.
func TestAVersionOverrideMayKeepItsDashes(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nmain:\n    FROM alpine:3.22\n    RUN echo hi\n",
		testMain, interp.WithVersionFlags([]string{"--use-function-keyword"}))
	if err != nil {
		t.Fatalf("a dashed override names a feature this engine has: %v", err)
	}
}
