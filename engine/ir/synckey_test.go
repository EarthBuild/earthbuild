package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `COPY --sync` is part of a step's key, because it changes what the step
// produces.
//
// **A flag that changes the output and not the key is the worst kind.** Two
// builds of one line, one skipping identical files and one rewriting them,
// produce different layers - different mtimes, and a delta holding the whole
// tree rather than the part that differs. Sharing a cache entry between them
// serves one build the other's answer, and nothing anywhere says so.
//
// Every other COPY flag that changes the result is already here; this test
// exists so the next one added is too.
func TestSyncIsPartOfTheKey(t *testing.T) {
	t.Parallel()

	plain := &ir.Node{Op: ir.Op{
		Kind: ir.OpFile,
		Args: []string{"src", "/app"},
	}}

	skipping := &ir.Node{Op: ir.Op{
		Kind: ir.OpFile,
		Args: []string{"src", "/app"},
		Sync: true,
	}}

	if plain.ID() == skipping.ID() {
		t.Error("--sync does not reach the key, so a build that skips" +
			" identical files and one that rewrites them are cached as one")
	}
}
