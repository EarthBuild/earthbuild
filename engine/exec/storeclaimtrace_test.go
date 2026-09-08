package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A refusal says where the claim it collided with was taken.
//
// **Because naming the holder was not enough.** The refusal already says `this
// build itself: a sandbox it started has not been stopped`, which identifies
// the process and not the sandbox - and two fixes aimed at plausible leaks
// changed the count not at all. What is missing is which sandbox, and which
// code path made it.
func TestARefusalSaysWhereTheClaimWasTaken(t *testing.T) {
	at := filepath.Join(t.TempDir(), "store.img")

	err := os.WriteFile(at, make([]byte, 4096), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	held, err := claimStore(at)
	if err != nil {
		t.Fatal(err)
	}

	defer held()

	_, err = claimStore(at)
	if err == nil {
		t.Fatal("a second claim on a held device succeeded")
	}

	// The test's own name is in the stack of whoever took the first claim, so
	// this asserts the trace is the claimer's rather than the refusal's.
	if !strings.Contains(err.Error(), "TestARefusalSaysWhereTheClaimWasTaken") {
		t.Errorf("the refusal does not say where the standing claim was taken,"+
			" so it names a process and not a sandbox:\n%v", err)
	}
}

// A released claim is forgotten, or the next refusal names a sandbox that has
// gone.
func TestAReleasedClaimIsForgotten(t *testing.T) {
	at := filepath.Join(t.TempDir(), "store.img")

	err := os.WriteFile(at, make([]byte, 4096), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	held, err := claimStore(at)
	if err != nil {
		t.Fatal(err)
	}

	held()

	if _, ok := claimedAt.Load(at); ok {
		t.Error("a released claim is still recorded, so a later refusal would" +
			" name a sandbox that had already gone")
	}
}
