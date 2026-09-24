package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestRunArgvIsWhatTheShellWillSee pins the exact argv a RUN produces.
//
// It exists because a change to expansion silently rewrote what commands ran:
// quotes were removed from `sh -c "echo x > /f"`, the redirect moved to the
// outer shell, and the build succeeded while writing an empty file. Every test
// passed, because none of them used a command whose *meaning* depended on
// quoting.
//
// Asserting the argv catches that class in milliseconds and without a sandbox.
// The end-to-end tests remain the proof that the argv is the right one; this is
// the proof it has not changed underneath us.
func TestRunArgvIsWhatTheShellWillSee(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		run  string
		want string
	}{
		{
			name: "nested shell keeps its quotes",
			run:  `/bin/busybox sh -c "echo hi > /f"`,
			want: `/bin/busybox sh -c "echo hi > /f"`,
		},
		{
			name: "a pipeline survives",
			run:  `sh -c "cat /a | tr a-z A-Z > /b"`,
			want: `sh -c "cat /a | tr a-z A-Z > /b"`,
		},
		{
			name: "single quotes survive",
			run:  `sh -c 'echo $NOT_OURS'`,
			want: `sh -c 'echo $NOT_OURS'`,
		},
		{
			name: "an undeclared variable is left for the shell",
			run:  `sh -c "for i in 1 2 3; do echo $i; done"`,
			want: `sh -c "for i in 1 2 3; do echo $i; done"`,
		},
		{
			name: "a redirect outside quotes stays outside",
			run:  `echo hello > /f`,
			want: `echo hello > /f`,
		},
		{
			name: "an escaped dollar is not a variable",
			run:  `echo \$5`,
			want: `echo \$5`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    RUN "+tc.run+"\n", "build")
			if err != nil {
				t.Fatal(err)
			}

			var argv []string

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind == ir.OpExec {
					argv = n.Op.Args
				}
			}

			if len(argv) != 3 || argv[0] != testShell || argv[1] != "-c" {
				t.Fatalf("argv is %q, want [/bin/sh -c <command>]", argv)
			}

			if argv[2] != tc.want {
				t.Errorf("the shell will run:\n  %s\nwant:\n  %s", argv[2], tc.want)
			}
		})
	}
}

// A declared argument *is* substituted, quoting and all - the point of the
// distinction is that only quoting is preserved, not that expansion stops.
func TestDeclaredArgumentsAreStillSubstitutedInCommands(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ARG target=/out
    RUN sh -c "echo hi > $target"
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if !strings.Contains(got, `"echo hi > /out"`) {
		t.Errorf("the declared argument was not substituted:\n%s", got)
	}
}

// A value the engine consumes loses its quotes, in the same build, so the two
// rules are visible together and cannot drift apart unnoticed.
func TestValuesAndCommandsAreTreatedDifferently(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testSpacedFile: "x"})

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    COPY "a file.txt" /dst
    RUN sh -c "echo done > /f"
`, "build", interp.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	var (
		copied string
		ran    string
	)

	for _, n := range p.Graph.Nodes() {
		// Partial on purpose: this collects the two kinds the case is about.
		switch n.Op.Kind { //nolint:exhaustive // partial on purpose, see above
		case ir.OpFile:
			copied = n.Op.Args[0]
		case ir.OpExec:
			ran = n.Op.Args[len(n.Op.Args)-1]
		}
	}

	// The path lost its quotes; the command kept its own.
	if copied != testSpacedFile {
		t.Errorf("the copied path is %q, want it unquoted", copied)
	}

	if !strings.Contains(ran, `"echo done > /f"`) {
		t.Errorf("the command lost its quoting: %s", ran)
	}
}
