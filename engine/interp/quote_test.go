package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Quotes are syntax, not part of the value.
//
// The grammar (earthfile.abnf) says `path` excludes quote characters unquoted
// and that "quoted paths permit QUOTED-STRING", so `COPY "a file.txt" /dst`
// names a file called `a file.txt`. Passing the quotes through produced
// `"a file.txt" is not in the build context` - a file nobody has, reported as
// the user's mistake.
func TestQuotedPathsAreUnquoted(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSpacedFile: "x", "plain.txt": "y"})

	for _, src := range []string{`"a file.txt"`, `'a file.txt'`, `plain.txt`} {
		t.Run(src, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    COPY "+src+" /dst\n",
				"build", interp.WithContext(ctx))
			if err != nil {
				t.Errorf("%s was not resolved: %v", src, err)
			}
		})
	}
}

// A quoted argument default is the value without its quotes.
func TestQuotedArgDefaults(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ARG greeting="hello world"
    RUN echo $greeting
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if !strings.Contains(got, "echo hello world") {
		t.Errorf("the quotes were kept in the value:\n%s", got)
	}
}

// An escaped character is the character. The grammar defines
// `escaped-char = "\" %x21-7E`, so `\$` is a literal dollar and not the start
// of a variable.
func TestEscapesAreResolved(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ARG price="\$5"
    RUN echo $price
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "$5") {
		t.Errorf("the escape was not resolved:\n%s", got)
	}
}

// Quotes inside a value survive: only the delimiters are syntax.
func TestInnerQuotesSurvive(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ARG msg="say \"hello\""
    RUN echo $msg
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, `say "hello"`) {
		t.Errorf("inner quotes were lost:\n%s", got)
	}
}

// A command line keeps its quoting; a value loses it.
//
// This is the distinction that matters, and getting it wrong is silent:
// `RUN sh -c "echo hi > /f"` with the quotes removed becomes
// `sh -c echo hi > /f`, where the redirect belongs to the *outer* shell and the
// inner one receives only `echo`. The build succeeds and writes an empty file.
//
// Quote removal is right for a value the engine consumes - a path, an argument
// default - and wrong for text a shell will parse again.
func TestCommandLinesKeepTheirQuoting(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN /bin/busybox sh -c "echo hi > /f"
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if !strings.Contains(got, `sh -c "echo hi > /f"`) {
		t.Errorf("the inner command lost its quoting:\n%s", got)
	}
}

// Variables are still expanded inside a command line - only the quoting is
// preserved.
func TestCommandLinesStillExpandVariables(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ARG name=world
    RUN sh -c "echo $name"
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if !strings.Contains(got, `"echo world"`) {
		t.Errorf("the variable was not expanded inside the command:\n%s", got)
	}
}
