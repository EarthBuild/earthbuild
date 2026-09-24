package exec

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step that was given a credential does not offer its caches to anybody.
//
// **The guarantee this design removed, put back the only way the host can.**
// C.3 said cache contents never leave the machine, so nothing has ever scanned
// one for secrets - only a step's delta is scanned, and only for the bytes as
// the step was given them. A portable cache breaks that promise.
//
// The host cannot do the scan: `layer.FindSecrets` needs the secret's *value*,
// which is staged inside the guest and deliberately never reaches this side.
// What the host knows is that the step was given one, and that is enough for the
// conservative answer - the cache stays here, the build elsewhere is slower, and
// nothing that held a credential crosses a wire.
//
// Over-cautious for `go build` with a registry token, and an author who wants
// that cache shared can put the secret in a different step. Which is a
// mechanical rule a reader can hold in their head, where "we scanned it and
// think it is fine" is not.
func TestAStepWithSecretsWithholdsItsCaches(t *testing.T) {
	t.Parallel()

	m := ir.Mount{ID: "k", Target: "/c", Portable: true, Helper: "./h.wasm"}

	var withheld []string

	e := &Executor{
		Mounts: "/s/mounts",
		Share: func(_ context.Context, _ ir.Mount, _, why string) error {
			withheld = append(withheld, why)

			return nil
		},
	}

	e.shareCaches(context.Background(), &ir.Node{Op: ir.Op{
		Kind: ir.OpExec, Mounts: []ir.Mount{m}, SecretEnv: []string{"TOKEN"},
	}})

	if len(withheld) != 1 {
		t.Fatalf("the hook was offered %d caches, want 1", len(withheld))
	}

	if withheld[0] == "" {
		t.Error("a step holding a secret offered its cache with no reservation" +
			"\n  the contents have never been scanned for one, because until now" +
			" they could not leave the machine")
	}

	if !strings.Contains(withheld[0], "secret") {
		t.Errorf("the reason does not say what it is: %q", withheld[0])
	}
}

// AWS credentials are the same question with a different name.
//
// They are outside the key for their own reason - session tokens are reissued
// constantly - and they are a credential the step was handed, which is what this
// is about.
func TestAStepWithAWSCredentialsWithholdsItsCaches(t *testing.T) {
	t.Parallel()

	var why string

	e := &Executor{
		Mounts: "/s/mounts",
		Share: func(_ context.Context, _ ir.Mount, _, reason string) error {
			why = reason

			return nil
		},
	}

	e.shareCaches(context.Background(), &ir.Node{Op: ir.Op{
		Kind: ir.OpExec, AWS: true,
		Mounts: []ir.Mount{{ID: "k", Target: "/c", Portable: true, Helper: "./h.wasm"}},
	}})

	if why == "" {
		t.Error("a step given AWS credentials offered its cache with no reservation")
	}
}

// An ordinary step offers its caches with nothing withheld.
func TestAStepWithoutSecretsSharesNormally(t *testing.T) {
	t.Parallel()

	var why string

	called := false

	e := &Executor{
		Mounts: "/s/mounts",
		Share: func(_ context.Context, _ ir.Mount, _, reason string) error {
			called, why = true, reason

			return nil
		},
	}

	e.shareCaches(context.Background(), &ir.Node{Op: ir.Op{
		Kind:   ir.OpExec,
		Mounts: []ir.Mount{{ID: "k", Target: "/c", Portable: true, Helper: "./h.wasm"}},
	}})

	if !called {
		t.Fatal("an ordinary step's cache was not offered at all")
	}

	if why != "" {
		t.Errorf("an ordinary step's cache was withheld: %q", why)
	}
}
