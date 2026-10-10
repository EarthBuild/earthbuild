package guest

import (
	"strings"
	"testing"
)

// TestChownIsRefusedWhereTheStoreDiscardsOwnership.
//
// **The same fault as `--keep-own`, failing the other way.** Both ask for files
// owned by somebody other than the invoking user; both are honoured inside the
// step and lost when the layer is committed to a store the host filesystem
// owns. `--keep-own` says so and refuses. `--chown` said nothing: on macOS
// `COPY --chown=1000:1000 f .` produced a file owned 0:0 and the build
// reported success, which is precisely the outcome the `--keep-own` refusal
// exists to prevent (green paper A2, I10).
//
// `tests/chown.earth` is the corpus case - it copies with `--chown` and then
// asserts `stat -c %U`, four lines later. A silent flag turns that into a
// puzzle about the file; a refusal names the store.
func TestChownIsRefusedWhereTheStoreDiscardsOwnership(t *testing.T) {
	t.Parallel()

	// A store that takes the request and keeps the invoking user's ownership,
	// which is what a share with no uids of its own does.
	discarding := func(string, int, int) error { return nil }

	err := checkStoreOwnership(t.TempDir(), "--chown", discarding)
	if err == nil {
		t.Skip("this filesystem carries ownership, so there is nothing to refuse")
	}

	// The guard is the same one, asked the same way: a caller that checks it
	// for --keep-own and not for --chown has two rules for one property.
	// Named by the flag that asked: `--chown` reaching a diagnostic about
	// `--keep-own` sends the reader after a flag their Earthfile does not use.
	for _, want := range []string{"--chown", "discards ownership", "layer store"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal reads %q, without %q", err, want)
		}
	}
}

// And the copy path asks for it, which is the half that was missing.
func TestACopyAsksAboutOwnershipForChownToo(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		opts copyOpts
		want bool
	}{
		{"keep-own", copyOpts{KeepOwn: true}, true},
		{"chown", copyOpts{Chown: "1000:1000"}, true},
		{"neither", copyOpts{}, false},
	} {
		if _, got := needsOwnershipInTheStore(c.opts); got != c.want {
			t.Errorf("%s: asks the store = %v, want %v"+
				"\n  both flags put a file in the image owned by somebody else,"+
				" and the store either carries that or does not", c.name, got, c.want)
		}
	}
}
