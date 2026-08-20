package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step that is given a docker daemon is not the same operation as one that is
// not, so it does not share a key with it.
//
// `RUN docker images` inside a WITH DOCKER block and the identical line outside
// one do different things - the first lists images, the second fails to find a
// command - and a cache that could not tell them apart would serve one for the
// other.
func TestNeedingDockerIsPartOfIdentity(t *testing.T) {
	t.Parallel()

	plain := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testDockerCommand}}}
	withDocker := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testDockerCommand}, Docker: true}}

	if plain.ID() == withDocker.ID() {
		t.Error("a step with a docker daemon shares a key with one without")
	}
}

// And two steps that both want one still agree.
func TestTwoDockerStepsAgree(t *testing.T) {
	t.Parallel()

	a := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testDockerCommand}, Docker: true}}
	b := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testDockerCommand}, Docker: true}}

	if a.ID() != b.ID() {
		t.Error("two identical steps in a WITH DOCKER block have different keys")
	}
}
