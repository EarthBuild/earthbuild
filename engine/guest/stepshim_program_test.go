package guest

import (
	"errors"
	"strings"
	"testing"
)

// A bare name is looked up on the PATH; a path is exec'd as written.
func TestResolveProgramLooksUpOnlyABareName(t *testing.T) {
	t.Parallel()

	got, err := resolveProgram("python3", func(string) (string, error) {
		return "/usr/bin/python3", nil
	})
	if err != nil || got != "/usr/bin/python3" {
		t.Errorf("resolveProgram(python3) = %q, %v", got, err)
	}

	// A path is the author's answer and is not second-guessed - `./configure`
	// means the one here, not whichever the PATH finds first.
	for _, name := range []string{"/usr/bin/env", "./configure", "sub/dir/tool"} {
		got, err = resolveProgram(name, func(string) (string, error) {
			t.Errorf("%s was looked up; it is already a path", name)

			return "", nil
		})
		if err != nil || got != name {
			t.Errorf("resolveProgram(%q) = %q, %v", name, got, err)
		}
	}

	// A name nothing can find says so, with the name in it - the shim's own
	// message is all the author gets, there being no shell to produce one.
	_, err = resolveProgram("nosuchtool", func(string) (string, error) {
		return "", errors.New("not found in $PATH")
	})
	if err == nil {
		t.Fatal("a name that resolves to nothing was accepted")
	}

	if !strings.Contains(err.Error(), "nosuchtool") {
		t.Errorf("the failure reads %q, without the name that could not be found", err)
	}
}
