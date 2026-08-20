package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A global argument is declared where globals live, and nowhere else.
//
// `tests/build-arg-explicit-global.earth` declares its globals in the base
// recipe and then has a target that exists to be refused:
//
//	test-failure:
//	    ARG --global global1=123
//
// A global declared inside a target is a contradiction: the base recipe is what
// every target starts from, so a "global" declared in one target could only
// reach the targets built after it - which is an ordering the language does not
// have and this engine does not model (E461).
//
// This engine accepted it, and what it did with it was worse than nothing: the
// name went into the globals map of a state that no other target inherits, so
// the author's `--global` decided nothing at all.
func TestAGlobalIsDeclaredInTheBaseRecipe(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    ARG --global g=1\n    RUN echo $g\n",
		testMain)
	if err == nil {
		t.Fatal("a target declared a global argument")
	}

	for _, want := range []string{"--global", "Earthfile:5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}
}

// And the base recipe still may.
func TestTheBaseRecipeMayDeclareAGlobal(t *testing.T) {
	t.Parallel()

	got := commandOfFirstExec(t, versioned+
		"\nFROM alpine:3.22\nARG --global g=1\n\nmain:\n    RUN echo $g\n")

	if !strings.HasSuffix(got, "echo 1") {
		t.Errorf("the step runs %q, and the base recipe's global reached it", got)
	}
}

// `PROJECT` needs the version that has it.
//
// `tests/project-secrets-without-flag.earth` is `VERSION 0.6`, writes
// `PROJECT org/project`, and says what it expects in the command it runs:
// *"should fail without --use-project-secrets VERSION flag"*. The construct
// arrived with that feature, and a file older than it is using a keyword its
// dialect does not have (E461).
func TestProjectNeedsTheVersionThatHasIt(t *testing.T) {
	t.Parallel()

	_, err := interp.Build("VERSION 0.6\n\nPROJECT org/project\n\n"+
		"main:\n    FROM alpine:3.22\n    RUN echo hi\n", testMain)
	if err == nil {
		t.Fatal("PROJECT was accepted by a file whose dialect does not have it")
	}

	if !strings.Contains(err.Error(), "PROJECT") {
		t.Errorf("refused with %q, which does not name the construct", err)
	}
}

// And a file that does have it is fine, with or without the flag written out.
func TestProjectIsFineAtTheVersionThatHasIt(t *testing.T) {
	t.Parallel()

	for _, version := range []string{
		"VERSION 0.8",
		"VERSION --use-project-secrets 0.6",
	} {
		_, err := interp.Build(version+"\n\nPROJECT org/project\n\n"+
			"main:\n    FROM alpine:3.22\n    RUN echo hi\n", testMain)
		if err != nil {
			t.Errorf("%s: %v", version, err)
		}
	}
}
