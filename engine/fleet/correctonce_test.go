package fleet

import (
	"os"
	"regexp"
	"testing"
)

// A reply's address is corrected at the one point every reply passes through.
//
// A worker announces where it can be reached and can be wrong about it - behind
// a NAT, or announcing an unspecified address - so the driver replaces the host
// with the one it saw the connection come from. Where that happens is the whole
// of E279: the first version corrected it in `note`, so the rendezvous knew the
// right address and `Delegating`, which keeps its own holder table from the raw
// reply, did not. One correction, one place, and everything downstream sees the
// same string.
//
// **A source guard, and the reason is worth stating.** The behaviour is
// invisible on one machine: the address a worker announces and the address it is
// seen from are the same, so `correctHost` returns its input and its absence
// changes nothing. A test that formed a real fleet here would pass with the
// correction deleted. What can be checked without two machines is that the call
// is still in the path every reply takes, which is what the mechanism is.
//
// `correctHost` itself is covered properly, by unit tests over announced and
// seen pairs that do differ.
func TestAReplysAddressIsCorrectedWhereEveryReplyPasses(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("rendezvous.go")
	if err != nil {
		t.Fatal(err)
	}

	src := string(b)

	// This file names the pattern in order to look for it.
	applied := regexp.MustCompile(`reply\.HeldAt\s*=\s*correctHost\(`)
	if !applied.MatchString(src) {
		t.Error("a reply's address is not corrected in rendezvous.go: a worker" +
			" that announces an address it cannot be reached at is dialled at" +
			" that address by everything downstream")
	}

	// Once on the reply, because two corrections are how the two tables
	// disagreed in the first place. The other call in this file is
	// `correctedForTest`, which is a helper and not a second correction.
	if n := len(applied.FindAllString(src, -1)); n != 1 {
		t.Errorf("the reply is corrected %d times, want 1: the defect this"+
			" replaced was a correction in one place and a raw address in"+
			" another", n)
	}
}
