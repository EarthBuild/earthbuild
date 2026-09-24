package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A secret's *value* never enters the graph.
//
// This is the property everything else rests on, and it is structural rather
// than filtered: the IR carries the secret's id and nothing else, so there is no
// field a value could reach and no key it could change. A design that carried
// the value and excluded it from the key would work until someone added a
// hasher, and the failure would be a credential in a cache key.
func TestASecretsValueNeverEntersTheGraph(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN --mount=type=secret,id=TOKEN,target=/run/token use-it
`, testMain, interp.WithSecrets(map[string]string{testSecret: "hunter2-the-actual-secret"}))
	if err != nil {
		t.Fatal(err)
	}

	// Everything the graph carries, in one string: the rendering a person would
	// read, every argument, every identity, and both maps a step holds. Order
	// does not matter because the question is whether the value appears at all.
	var rendered strings.Builder

	rendered.WriteString(describe(p.Graph.Nodes()))

	for _, n := range p.Graph.Nodes() {
		rendered.WriteString(strings.Join(n.Op.Args, " "))
		rendered.WriteString(n.ID().String())

		for _, m := range n.Op.Mounts {
			rendered.WriteString(m.ID + m.Target)
		}

		for k, v := range n.Op.Env {
			rendered.WriteString(k + v)
		}
	}

	if strings.Contains(rendered.String(), "hunter2") {
		t.Error("the secret's value is somewhere in the graph")
	}
}

// A step given a secret is not cached.
//
// Its output may depend on the secret, and the secret is deliberately not in
// the key - so there is no honest key for the result, the same reasoning that
// applies to a cache mount (I3) and to a host step (I7).
func TestAStepWithASecretIsNotCached(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --mount=type=secret,id=TOKEN,target=/run/token use\n",
		testMain, interp.WithSecrets(map[string]string{testSecret: "value"}))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if len(n.Op.Mounts) > 0 && !n.Op.NoCache {
			t.Error("a step given a secret is marked cacheable")
		}
	}
}

// A secret nobody supplied is refused, naming it.
//
// Running with an empty file instead is the worst available answer: the command
// fails somewhere far from the line that asked for the credential, usually with
// a message about authentication that sends the reader to the wrong system.
func TestAnUnsuppliedSecretIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --mount=type=secret,id=TOKEN,target=/run/token use\n",
		testMain)
	if err == nil {
		t.Fatal("a secret nobody supplied was accepted")
	}

	for _, want := range []string{testSecret, "--secret"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}
