package corpus

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The tree says how each of its files is meant to be run.
//
// `tests/Earthfile` drives the corpus with 286 invocations of its own
// `RUN_EARTH` function, naming 108 of the 116 files:
//
//	DO +RUN_EARTH --earthfile=privileged.earth --extra_args="--allow-privileged" --target=+test
//	DO +RUN_EARTH --earthfile=copy.earth --target=+copy-wildcard
//
// The gate had been guessing - the file's `all`, or its `test`, or its first
// target - which is one target per file and the wrong one wherever a file
// declares a helper first (E445). The tree knows: it names the target, and the
// arguments the target needs (E454).
//
// **Parsed rather than reimplemented.** What is extracted is what the line says;
// an invocation this does not understand is reported rather than guessed at,
// because a gate that quietly drops the ones it cannot read is a gate that
// reports a smaller tree than it was given.
func TestReadingHowTheTreeRunsItsOwnFiles(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		line string
		want Invocation
	}{{
		name: "a file and a target",
		line: `    DO +RUN_EARTH --earthfile=copy.earth --target=+copy-wildcard`,
		want: Invocation{File: "copy.earth", Target: "copy-wildcard"},
	}, {
		name: "arguments the target needs",
		line: `    DO +RUN_EARTH --earthfile=privileged.earth --extra_args="--allow-privileged" --target=+test`,
		want: Invocation{
			File: "privileged.earth", Target: "test",
			Extra: []string{"--allow-privileged"},
		},
	}, {
		name: "a build argument with a value",
		line: `    DO +RUN_EARTH --target=+t --extra_args="--build-arg EXPECTED_VALUE=false"`,
		want: Invocation{
			File: "", Target: "t",
			Extra: []string{"--build-arg", "EXPECTED_VALUE=false"},
		},
	}, {
		// The default target, which the reference spells `+base`-less: an
		// invocation naming no target builds the file's first one, and saying so
		// here keeps the gate's rule in one place.
		name: "no target named",
		line: `    DO +RUN_EARTH --earthfile=comments.earth`,
		want: Invocation{File: "comments.earth"},
	}, {
		// The tree's own account of a negative target. Seventy-seven invocations
		// say this, and the gate had been counting every one of them as a
		// failure to build - which is how `allow-privileged.earth`, a file whose
		// whole purpose is to be refused, read as an engine defect (E455).
		name: "a target that is supposed to fail",
		line: `    DO +RUN_EARTH --earthfile=fail.earth --target=+test --should_fail=true`,
		want: Invocation{File: "fail.earth", Target: "test", ShouldFail: true},
	}, {
		name: "not an invocation at all",
		line: `    RUN echo hello`,
		want: Invocation{},
	}} {
		got := readInvocation(tc.line)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: read %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// Every invocation in the tree is either understood or reported.
//
// The count is the point: 286 lines, and a parser that reads 200 of them is a
// gate that has quietly stopped looking at a third of the corpus.
func TestEveryInvocationInTheTreeIsUnderstood(t *testing.T) {
	t.Parallel()

	src := treeSrc(t)

	var seen, understood int

	for _, line := range statements(src) {
		if !strings.Contains(line, "+RUN_EARTH") {
			continue
		}

		seen++

		// Understood means it named something: a file, a target, or arguments.
		// Compared field by field because the type holds a slice, and a line
		// this reads as nothing at all is the case worth counting.
		got := readInvocation(line)
		if got.File != "" || got.Target != "" || len(got.Extra) > 0 || got.Exec != "" {
			understood++
		}
	}

	if seen < 200 {
		t.Fatalf("found %d invocations in tests/Earthfile, and it has hundreds"+
			"\n  the gate is reading the wrong file, or reading it wrongly", seen)
	}

	if understood < seen {
		t.Errorf("%d of %d invocations were not understood, so that much of the"+
			" tree is not being driven the way it says it should be",
			seen-understood, seen)
	}
}

// An invocation naming no file inherits the last one.
//
// `RUN_EARTH` copies the named file to `Earthfile` inside the container, and the
// invocations after it in the same target reuse what is there:
//
//	DO +RUN_EARTH --earthfile=from-dockerfile-dockerignore.earth --target=+create-files
//	DO +RUN_EARTH --target=+image
//
// Read as "no file means tests/Earthfile", the second built the tree's own
// Earthfile and looked for a target it does not have - eight invocations failing
// with `no target named "image"`, which is the harness's mistake reported as the
// engine's (E470).
//
// A target header resets it: a new target starts from the base recipe, and
// whatever an earlier target copied is not there.
func TestAnInvocationInheritsTheLastFileNamed(t *testing.T) {
	t.Parallel()

	got := Invocations(`one:
    DO +RUN_EARTH --earthfile=a.earth --target=+first
    DO +RUN_EARTH --target=+second

two:
    DO +RUN_EARTH --target=+third
`)

	if len(got) != 3 {
		t.Fatalf("read %d invocations, want 3", len(got))
	}

	for i, want := range []Invocation{
		{File: "a.earth", Target: "first"},
		{File: "a.earth", Target: "second"},
		{File: "", Target: "third"},
	} {
		if got[i].File != want.File || got[i].Target != want.Target {
			t.Errorf("invocation %d is %+v, want %+v", i, got[i], want)
		}
	}
}

// A target flag may carry the target's own arguments.
//
// `--target="+create-files --with_docker_ignore=\"true\""` is one flag holding
// two things, and reading only the first word drops an argument the target needs
// (E470).
func TestATargetFlagMayCarryArguments(t *testing.T) {
	t.Parallel()

	got := readInvocation(
		`    DO +RUN_EARTH --earthfile=a.earth --target="+t --flag=value"`)

	if got.Target != "t" {
		t.Errorf("target is %q, want t", got.Target)
	}

	if len(got.Extra) != 1 || got.Extra[0] != "--flag=value" {
		t.Errorf("extra is %v, want the target's own argument", got.Extra)
	}
}

// treeSrc reads `tests/Earthfile`, or skips where there is no corpus.
func treeSrc(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "Earthfile"))
	if err != nil {
		t.Skipf("no corpus here: %v", err)
	}

	return string(b)
}
