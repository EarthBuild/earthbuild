package guest

import (
	"strings"
	"syscall"
	"testing"
)

// The tail keeps the end, which is where the reason is.
//
// A daemon that fails prints its startup chatter first and the reason last, so a
// buffer that keeps the *first* 2KB keeps exactly the part nobody needs - and it
// would look identical in every test that only checks the buffer is non-empty.
func TestTheTailKeepsTheEndNotTheBeginning(t *testing.T) {
	t.Parallel()

	var keep tail

	_, err := keep.Write([]byte(strings.Repeat("chatter\n", 1000)))
	if err != nil {
		t.Fatal(err)
	}

	_, err = keep.Write([]byte("and here is the reason"))
	if err != nil {
		t.Fatal(err)
	}

	got := keep.String()

	if !strings.HasSuffix(got, "and here is the reason") {
		t.Errorf("the reason was dropped; the tail ends with %q",
			got[max(0, len(got)-40):])
	}

	if len(got) > tailKeeps {
		t.Errorf("the tail is %d bytes and is supposed to be bounded at %d",
			len(got), tailKeeps)
	}
}

// A container that will not let the shim mount says what is missing.
//
// The failure nesting will actually hit. An inner build - `earth` inside a WITH
// DOCKER step - runs in a container that is root but has no `CAP_SYS_ADMIN`, so
// the private `/run` cannot be mounted. `mount: operation not permitted` sends
// the author to the wrong question entirely.
func TestAContainerThatCannotMountSaysWhatIsMissing(t *testing.T) {
	t.Parallel()

	got := sysAdminHint(syscall.EPERM)

	for _, want := range []string{"CAP_SYS_ADMIN", "container"} {
		if !strings.Contains(got, want) {
			t.Errorf("the hint does not mention %q:\n%s", want, got)
		}
	}

	// Only for the one error it explains. A hint under every failure is a hint
	// nobody reads - the rule `startHint` already follows.
	if sysAdminHint(syscall.ENOSPC) != "" {
		t.Errorf("a hint was offered for an error it does not explain:\n%s",
			sysAdminHint(syscall.ENOSPC))
	}
}
