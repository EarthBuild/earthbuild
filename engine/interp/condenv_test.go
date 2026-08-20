package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A name the plan does not know is run, not refused.
//
// `IF [ "$CARGO_HOME" = "" ]` is the corpus's case: CARGO_HOME is set by the
// base image, and no ARG declares it. Refusing it as undeclared is a false
// refusal - the name exists, in the one place this engine has not looked. A
// probe answers it exactly as the step's own shell would.
func TestAnUnknownNameInAConditionIsRun(t *testing.T) {
	t.Parallel()

	r := &recorder{result: true}

	p, err := interp.Build(versioned+`
main:
    FROM rust:1.90
    IF [ "$CARGO_HOME" = "" ]
        RUN set-cargo-home
    END
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.calls) == 0 {
		t.Fatal("the condition was decided without asking the build environment")
	}

	if !strings.Contains(describe(p.Graph.Nodes()), "set-cargo-home") {
		t.Errorf("the answer did not pick the branch:\n%s", describe(p.Graph.Nodes()))
	}
}

// Without anywhere to run it, the refusal says that rather than blaming the
// Earthfile for an argument it never had to declare.
func TestAnUnknownNameWithoutARunnerIsAProbeRefusal(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM rust:1.90
    IF [ "$CARGO_HOME" = "" ]
        RUN set-cargo-home
    END
`, testMain)
	if err == nil {
		t.Fatal("a condition over an unknown name was decided anyway")
	}

	if !errors.Is(err, interp.ErrNoRunner) {
		t.Errorf("not reported as needing a runner:\n%s", err)
	}
}

// What the plan does know, it decides itself - and does not pay for a probe.
//
// A probe costs a round trip to a sandbox and blocks interpretation while it
// runs, so falling back to one for a value already in hand would make every
// build slower for nothing.
func TestAKnownNameIsDecidedWithoutRunningAnything(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, source, want string }{
		{"a declared argument", `
main:
    FROM alpine:3.22
    ARG flavour = plain
    IF [ "$flavour" = "plain" ]
        RUN plain-build
    END
`, "plain-build"},
		{"a variable set by ENV", `
main:
    FROM alpine:3.22
    ENV CARGO_HOME=/opt/cargo
    IF [ "$CARGO_HOME" = "/opt/cargo" ]
        RUN use-opt-cargo
    END
`, "use-opt-cargo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &recorder{result: false}

			p, err := interp.Build(versioned+tc.source, testMain, interp.WithCommands(r.run))
			if err != nil {
				t.Fatal(err)
			}

			if len(r.calls) != 0 {
				t.Errorf("a condition the plan could decide was sent to a sandbox: %q", r.calls)
			}

			if !strings.Contains(describe(p.Graph.Nodes()), tc.want) {
				t.Errorf("%q is not in the plan:\n%s", tc.want, describe(p.Graph.Nodes()))
			}
		})
	}
}
