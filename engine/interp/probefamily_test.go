package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Every construct whose value can be a `$(...)` gets it expanded.
//
// The corpus reports each of these separately - "SAVE IMAGE ... has to be run
// to know its value", "LET ...", "FOR ..." - which reads like a list of
// unimplemented commands. It is one mechanism, and this is the test that says
// so: the expansion is general to every command except RUN, ENTRYPOINT and CMD,
// which are handed to a shell whose job it already is.
func TestEveryConstructExpandsACommand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		source string
		want   string // what must appear in the plan once expanded
	}{
		{
			testCmdSaveImage, `
main:
    FROM alpine:3.22
    SAVE IMAGE app:$(cat version)
`, "app:1.2.3",
		},
		{
			testCmdLet, `
main:
    FROM alpine:3.22
    LET v = $(cat version)
    RUN echo $v
`, testVersion,
		},
		{
			"ARG", `
main:
    FROM alpine:3.22
    ARG v = $(cat version)
    RUN echo $v
`, testVersion,
		},
		{
			testCmdSaveArtifact, `
main:
    FROM alpine:3.22
    RUN make
    SAVE ARTIFACT /out/$(cat version)
`, testVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &recorder{result: true, output: "1.2.3\n"}

			p, err := interp.Build(versioned+tc.source, testMain, interp.WithCommands(r.run))
			if err != nil {
				t.Fatal(err)
			}

			if len(r.calls) == 0 {
				t.Fatal("nothing was run to find the value")
			}

			if got := strings.Join(r.calls[0], " "); got != "cat version" {
				t.Errorf("ran %q, want %q", got, "cat version")
			}

			if !strings.Contains(plainText(p), tc.want) {
				t.Errorf("%q is not in the plan:\n%s", tc.want, plainText(p))
			}
		})
	}
}

// plainText is everything the plan says, so a test can ask whether an expanded
// value reached it without knowing which field it lands in - the point being
// that one mechanism feeds an image reference, an artifact path and a variable
// alike.
func plainText(p *interp.Plan) string {
	var b strings.Builder

	b.WriteString(describe(p.Graph.Nodes()))

	for _, img := range p.Images {
		b.WriteString("\nSAVE IMAGE " + img.Ref)
	}

	for _, a := range p.Artifacts {
		b.WriteString("\nSAVE ARTIFACT " + a.Path + " " + a.LocalDest)
	}

	return b.String()
}

// The expansion runs on the build state at that line, not on a fresh image.
//
// `LET v = $(cat version)` reads a file that an earlier RUN produced. Running
// it against the target's base would read a file that does not exist yet and
// either fail or - worse - find a stale one from the image.
func TestAProbeSeesTheStepsAboveIt(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "1.2.3\n"}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN generate-version > version
    LET v = $(cat version)
    RUN echo $v
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.bases) == 0 || r.bases[0] == nil {
		t.Fatal("the probe was given no build state to run against")
	}

	if !reaches(r.bases[0], "RUN generate-version > version") {
		t.Errorf("the probe does not stand on the step that made the file it reads:\n%s",
			describe([]*ir.Node{r.bases[0]}))
	}
}

// reaches reports whether a step is at or below n.
func reaches(n *ir.Node, description string) bool {
	if n == nil {
		return false
	}

	if n.Meta.Description == description {
		return true
	}

	for _, in := range n.Inputs {
		if reaches(in, description) {
			return true
		}
	}

	return false
}

// A value nobody can supply says which command it needed and how to build it
// anyway, rather than leaving the text unexpanded in the result.
func TestWithoutARunnerTheRefusalNamesTheCommand(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    SAVE IMAGE app:$(cat version)
`, testMain)
	if err == nil {
		t.Fatal("a value that needed running was produced without running it")
	}

	for _, want := range []string{testCmdSaveImage, "cat version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// A supplied argument does not run its default's command.
//
// `ARG v = $(git describe --tags)` in a target the caller always passes `v` to
// would otherwise run a command whose answer is thrown away - and in the build
// where this matters, the command does not work at all: the default exists
// precisely because the tool is absent, or the file is not there yet. A
// discarded value is cheap; a discarded *failure* stops the build.
func TestASuppliedArgumentSkipsItsDefaultsCommand(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true, output: "1.2.3\n"}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG v = $(this-would-fail)
    RUN echo $v
`, testMain, interp.WithCommands(r.run), interp.WithArgs(map[string]string{"v": "9.9.9"}))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.calls) != 0 {
		t.Errorf("the default's command ran anyway: %q", r.calls)
	}

	if !strings.Contains(plainText(p), "9.9.9") {
		t.Errorf("the supplied value is not in the plan:\n%s", plainText(p))
	}
}

// A refusal for want of a runner is distinguishable from an unimplemented
// construct, by type rather than by reading the message.
//
// They are different kinds of number and adding them up overstates the work
// left: an unimplemented construct is work, whereas `LET v = $(cat version)`
// is finished and simply cannot be answered by a caller that planned without
// somewhere to run things. The corpus plans exactly that way, so without this
// distinction every probe in the corpus was counted as a missing feature.
func TestAMissingRunnerIsItsOwnKindOfRefusal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, source string }{
		{"a value", `
main:
    FROM alpine:3.22
    LET v = $(cat version)
    RUN echo $v
`},
		{"a condition", `
main:
    FROM alpine:3.22
    IF command -v unbuffer
        RUN yes
    END
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+tc.source, testMain)
			if err == nil {
				t.Fatal("planned without the runner it needed")
			}

			if !errors.Is(err, interp.ErrNoRunner) {
				t.Errorf("not reported as a missing runner:\n%s", err)
			}
		})
	}
}

// An actually-unimplemented construct is not mistaken for one.
func TestAnUnimplementedConstructIsNotAMissingRunner(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER
        RUN docker ps
    END
`, testMain)
	if err == nil {
		t.Skip("WITH DOCKER is implemented; pick another unimplemented construct here")
	}

	if errors.Is(err, interp.ErrNoRunner) {
		t.Errorf("an unimplemented construct was reported as a missing runner:\n%s", err)
	}
}
