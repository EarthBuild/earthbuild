package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

func planWith(t *testing.T, src string) string {
	t.Helper()

	p, err := interp.Build(versioned+src, "build")
	if err != nil {
		t.Fatal(err)
	}

	return describe(p.Graph.Nodes())
}

const branching = `
build:
    FROM alpine:3.22
    ARG mode=debug
    IF [ "$mode" = "release" ]
        RUN build-release
    ELSE
        RUN build-debug
    END
`

// A condition over build arguments is decided when the plan is made.
//
// This is what almost every IF in real Earthfiles is: a string comparison of an
// argument. Deciding it here keeps the graph *known before the build*, which is
// what every key, every schedule and every diagnostic in this engine depends on.
func TestArgumentConditionsAreDecidedAtPlanTime(t *testing.T) {
	t.Parallel()

	if got := planWith(t, branching); !strings.Contains(got, "build-debug") {
		t.Errorf("the false branch was not taken:\n%s", got)
	}

	if got := planWith(t, branching); strings.Contains(got, "build-release") {
		t.Errorf("the untaken branch is in the graph:\n%s", got)
	}
}

// And the other way, when the argument says so.
func TestTheTrueBranchIsTakenWhenTheConditionHolds(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+branching, "build",
		interp.WithArgs(map[string]string{"mode": "release"}))
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if !strings.Contains(got, "build-release") || strings.Contains(got, "build-debug") {
		t.Errorf("the wrong branch was taken:\n%s", got)
	}
}

// The branch changes the graph, so it changes the key. An IF that produced one
// step whichever way it went would be a false hit wearing a conditional.
func TestBranchesProduceDifferentGraphs(t *testing.T) {
	t.Parallel()

	mk := func(mode string) string {
		p, err := interp.Build(versioned+branching, "build",
			interp.WithArgs(map[string]string{"mode": mode}))
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID().String()
	}

	if mk("debug") == mk("release") {
		t.Error("two branches produced the same graph")
	}
}

// The forms that actually appear in Earthfiles.
func TestConditionForms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		cond string
		want string
	}{
		{`[ "$v" = "yes" ]`, testTakenMark},
		{`[ "$v" != "no" ]`, testTakenMark},
		{`[ -n "$v" ]`, testTakenMark},
		{`[ -z "$v" ]`, testSkippedMark},
		{`[[ "$v" = "yes" ]]`, testTakenMark},
		{`[ $v = yes ]`, testTakenMark},
		{`true`, testTakenMark},
		{`false`, testSkippedMark},
	} {
		t.Run(tc.cond, func(t *testing.T) {
			t.Parallel()

			got := planWith(t, `
build:
    FROM alpine:3.22
    ARG v=yes
    IF `+tc.cond+`
        RUN yes-branch
    ELSE
        RUN no-branch
    END
`)
			if !strings.Contains(got, tc.want) {
				t.Errorf("%s did not take the %s:\n%s", tc.cond, tc.want, got)
			}
		})
	}
}

// Tests joined by `&&` and `||` are still a function of the build arguments,
// so they are decided rather than refused.
//
// This is the commonest shape in the corpus that was being turned away: nine of
// the eleven conditions the engine refused were chains of comparisons over
// arguments, and none of them needed a process. The two that remained -
// `command -v unbuffer` and `sleep 5` - are the ones that genuinely do.
func TestChainedConditions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		cond string
		want string
	}{
		{`[ "$v" = "yes" ] && [ "$w" != "yes" ]`, testTakenMark},
		{`[ "$v" = "yes" ] && [ "$w" = "yes" ]`, testSkippedMark},
		{`[ "$v" = "no" ] || [ "$w" = "no" ]`, testTakenMark},
		{`[ "$v" = "no" ] || [ "$w" = "yes" ]`, testSkippedMark},
		// Left-associative and equal precedence, as the shell has them:
		// (false && false) || true.
		{`[ "$v" = "no" ] && [ "$w" = "no" ] || [ "$v" = "yes" ]`, testTakenMark},
		// An operand the engine could not decide alone is never reached, and
		// what is not evaluated needs no decision - which is the shell's rule
		// too, not a liberty taken to widen coverage.
		{`[ "$v" = "no" ] && command -v unbuffer`, testSkippedMark},
		{`[ "$v" = "yes" ] || command -v unbuffer`, testTakenMark},
	} {
		t.Run(tc.cond, func(t *testing.T) {
			t.Parallel()

			got := planWith(t, `
build:
    FROM alpine:3.22
    ARG v=yes
    ARG w=no
    IF `+tc.cond+`
        RUN yes-branch
    ELSE
        RUN no-branch
    END
`)
			if !strings.Contains(got, tc.want) {
				t.Errorf("%s did not take the %s:\n%s", tc.cond, tc.want, got)
			}
		})
	}
}

// An operand that expanded to nothing compares as empty.
//
// The parser drops an empty token, so `[ "$missing" = js ]` arrives as `= js`
// with the left side absent. Absent is what empty looks like after expansion,
// and the author plainly meant a comparison - the same reasoning the `-z` cases
// already rest on.
func TestConditionsOnOperandsThatExpandedToNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		cond string
		want string
	}{
		{`[ "$missing" = "js" ]`, testSkippedMark},
		{`[ "$missing" != "js" ]`, testTakenMark},
		{`[ "js" = "$missing" ]`, testSkippedMark},
	} {
		t.Run(tc.cond, func(t *testing.T) {
			t.Parallel()

			got := planWith(t, `
build:
    FROM alpine:3.22
    ARG missing=
    IF `+tc.cond+`
        RUN yes-branch
    ELSE
        RUN no-branch
    END
`)
			if !strings.Contains(got, tc.want) {
				t.Errorf("%s did not take the %s:\n%s", tc.cond, tc.want, got)
			}
		})
	}
}

// ELSE IF chains pick the first that holds.
func TestElseIfChains(t *testing.T) {
	t.Parallel()

	got := planWith(t, `
build:
    FROM alpine:3.22
    ARG v=b
    IF [ "$v" = "a" ]
        RUN branch-a
    ELSE IF [ "$v" = "b" ]
        RUN branch-b
    ELSE
        RUN branch-c
    END
`)

	if !strings.Contains(got, "branch-b") {
		t.Errorf("the matching branch was not taken:\n%s", got)
	}

	for _, no := range []string{"branch-a", "branch-c"} {
		if strings.Contains(got, no) {
			t.Errorf("%s should not be in the graph:\n%s", no, got)
		}
	}
}

// A condition that needs to run a command is refused, and says so.
//
// `IF command -v unbuffer` cannot be decided without a filesystem, so deciding
// it would mean guessing. Guessing a branch builds something the Earthfile does
// not describe, and reports success.
func TestConditionsNeedingExecutionAreRefused(t *testing.T) {
	t.Parallel()

	for _, cond := range []string{`command -v unbuffer`, `[ -f /etc/passwd ]`, `sleep 5`} {
		t.Run(cond, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    IF `+cond+`
        RUN yes
    END
`, "build")
			if err == nil {
				t.Fatalf("%q was decided without running it", cond)
			}

			if !strings.Contains(err.Error(), "Earthfile:") {
				t.Errorf("the refusal does not say where:\n%s", err)
			}

			if !strings.Contains(err.Error(), "buildkit") {
				t.Errorf("the refusal offers no alternative:\n%s", err)
			}
		})
	}
}

// A condition mentioning something never declared is refused rather than
// treated as empty: the author meant something by it.
func TestConditionsOnUndeclaredValuesAreRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    IF [ "$undeclared" = "x" ]
        RUN yes
    END
`, "build")
	if err == nil {
		t.Fatal("a condition on an undeclared variable was decided")
	}

	if !strings.Contains(err.Error(), "undeclared") {
		t.Errorf("the refusal does not name the variable:\n%s", err)
	}
}

// `!` negates a condition, and appears 36 times in this repository.
func TestNegatedConditions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ cond, want string }{
		{`[ ! -z "$v" ]`, testTakenMark},
		{`[ ! -n "$v" ]`, testSkippedMark},
		{`[ ! "$v" = "no" ]`, testTakenMark},
		{`[ ! "$v" = "yes" ]`, testSkippedMark},
	} {
		t.Run(tc.cond, func(t *testing.T) {
			t.Parallel()

			got := planWith(t, `
build:
    FROM alpine:3.22
    ARG v=yes
    IF `+tc.cond+`
        RUN yes-branch
    ELSE
        RUN no-branch
    END
`)
			if !strings.Contains(got, tc.want) {
				t.Errorf("%s did not take the %s:\n%s", tc.cond, tc.want, got)
			}
		})
	}
}

// An argument that expands to nothing still decides a condition: `[ -z "$x" ]`
// with x unset is true, and that is the whole reason people write it.
func TestConditionsOnEmptyArguments(t *testing.T) {
	t.Parallel()

	got := planWith(t, `
build:
    FROM alpine:3.22
    ARG v=
    IF [ -z "$v" ]
        RUN empty
    ELSE
        RUN not-empty
    END
`)

	if !strings.Contains(got, "RUN empty") {
		t.Errorf("an empty argument did not satisfy -z:\n%s", got)
	}
}

// A flag on IF is a flag, not part of the condition.
//
// The same defect RUN had: `IF --no-cache [ "$x" = y ]` was decided by reading
// `--no-cache` as the first word of the condition, so a condition that is
// perfectly decidable looked like one needing a command. A condition's flags
// govern how it is *evaluated*, never what it says.
func TestIfFlagsAreNotPartOfTheCondition(t *testing.T) {
	t.Parallel()

	for _, cond := range []string{
		`--no-cache [ "$v" = "yes" ]`,
		`[ "$v" = "yes" ]`,
	} {
		t.Run(cond, func(t *testing.T) {
			t.Parallel()

			got := planWith(t, `
build:
    FROM alpine:3.22
    ARG v=yes
    IF `+cond+`
        RUN yes-branch
    ELSE
        RUN no-branch
    END
`)
			if !strings.Contains(got, testTakenMark) {
				t.Errorf("%s did not take the true branch:\n%s", cond, got)
			}
		})
	}
}

// A flag that changes what the condition may do is refused, not stripped.
func TestSemanticIfFlagsAreRefused(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{testPrivilegedFlag, "--secret=TOKEN", testSSHFlag} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    IF `+flag+` [ -f x ]
        RUN yes-branch
    END
`, "build")
			if err == nil {
				t.Fatalf("IF %s was accepted and its flag ignored", flag)
			}

			name, _, _ := strings.Cut(flag, "=")
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name %s:\n%s", name, err)
			}
		})
	}
}
