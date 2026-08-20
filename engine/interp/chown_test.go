package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `COPY --chown` reaches the step, as written.
//
// The specification travels rather than a resolved pair, because the names mean
// whatever the *destination image* says they mean and only the guest has that
// image (A3). It is also what the key should describe: the Earthfile said
// `www-data`, and two images resolving that differently are two results from one
// file (E419).
func TestChownReachesTheStepAsWritten(t *testing.T) {
	t.Parallel()

	dir := withFile(t)

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    COPY --chown=testuser:testgroup ./a.txt /x
`, "build", interp.WithContext(dir))
	if err != nil {
		t.Fatalf("%v", err)
	}

	var found bool

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Chown != "" {
			found = true

			if n.Op.Chown != "testuser:testgroup" {
				t.Errorf("the step carries %q, not what the Earthfile wrote", n.Op.Chown)
			}
		}
	}

	if !found {
		t.Error("no step carries the ownership the COPY asked for")
	}
}

// Changing the owner changes the step.
func TestChangingTheChownChangesTheKey(t *testing.T) {
	t.Parallel()

	dir := withFile(t)

	key := func(who string) ir.NodeID {
		t.Helper()

		plan, err := interp.Build("VERSION 0.8\nbuild:\n    FROM alpine\n"+
			"    COPY --chown="+who+" ./a.txt /x\n", "build",
			interp.WithContext(dir))
		if err != nil {
			t.Fatalf("%v", err)
		}

		return plan.Graph.Root.ID()
	}

	if key("alice") == key("bob") {
		t.Error("two copies landing as different users share a key")
	}
}

// Asking for both owners is refused.
func TestChownAndKeepOwnTogetherAreRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    COPY --chown=testuser --keep-own ./a.txt /x
`, "build", interp.WithContext(withFile(t)))
	if err == nil {
		t.Fatal("a copy asking for two different owners was accepted")
	}

	if !strings.Contains(err.Error(), "--keep-own") {
		t.Errorf("the refusal does not name the contradiction: %v", err)
	}
}

// withFile is a build context holding the file these copies name.
func withFile(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}
