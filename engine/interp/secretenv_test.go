package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `RUN --secret TOKEN` gives a step a credential as an environment variable.
//
// The name is on the operation and reaches the key; the value is not there at
// all. Env *is* hashed, which is why the value cannot simply be put in it: a
// credential in `Op.Env` is a credential in the cache key, and the key is
// written to disk and shared between machines.
func TestASecretEnvNameIsKeyedAndItsValueIsAbsent(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --secret TOKEN use-it\n",
		testMain, interp.WithSecrets(map[string]string{testSecret: "hunter2-the-actual-secret"}))
	if err != nil {
		t.Fatal(err)
	}

	var seen bool

	for _, n := range p.Graph.Nodes() {
		if len(n.Op.SecretEnv) == 0 {
			continue
		}

		seen = true

		if n.Op.SecretEnv[0] != testSecret {
			t.Errorf("the step asks for %q", n.Op.SecretEnv[0])
		}

		if !n.Op.NoCache {
			t.Error("a step given a secret is marked cacheable")
		}

		for k, v := range n.Op.Env {
			if strings.Contains(k+v, "hunter2") {
				t.Error("the secret's value is in the step's environment, and so in its key")
			}
		}
	}

	if !seen {
		t.Fatalf("no step records the secret:\n%s", describe(p.Graph.Nodes()))
	}
}

// `RUN --secret NAME=SOURCE` takes the value from a differently-named secret.
func TestASecretEnvCanBeRenamed(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --secret TOKEN=CI_TOKEN use-it\n",
		testMain, interp.WithSecrets(map[string]string{"CI_TOKEN": "value"}))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if len(n.Op.SecretEnv) > 0 && n.Op.SecretEnv[0] != "TOKEN=CI_TOKEN" {
			t.Errorf("the step records %q, want the pair as written", n.Op.SecretEnv[0])
		}
	}
}

// A secret nobody supplied is refused, by the name it would have come from.
func TestAnUnsuppliedSecretEnvIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --secret TOKEN=CI_TOKEN use-it\n", testMain)
	if err == nil {
		t.Fatal("a secret nobody supplied was accepted")
	}

	if !strings.Contains(err.Error(), "CI_TOKEN") {
		t.Errorf("the refusal names the wrong secret:\n%s", err)
	}
}
