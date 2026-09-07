package guest

import (
	"strings"
	"testing"
)

// A path is exec'd as written; a bare name that got this far is a failure with
// something useful to say.
//
// The *lookup* is `lookIn`'s, in the guest, before the step's thread carries a
// seccomp filter - see resolveProgram.
func TestOnlyAPathReachesTheExec(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"/usr/bin/env", "./configure", "sub/dir/tool"} {
		err := resolveProgram(name)
		if err != nil {
			t.Errorf("resolveProgram(%q) = %v, and it is already a path", name, err)
		}
	}

	err := resolveProgram("python3")
	if err == nil {
		t.Fatal("a bare name was accepted; nothing after this point resolves one")
	}

	if !strings.Contains(err.Error(), "python3") {
		t.Errorf("the failure reads %q, without the name that could not be found", err)
	}

	err = resolveProgram("")
	if err == nil {
		t.Error("an empty command was accepted")
	}
}
