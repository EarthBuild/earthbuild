package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// A step may hand the engine a script to run before its daemon starts.
//
// `buildkitd/dockerd-wrapper.sh` runs
// `/usr/share/earthly/dockerd-wrapper-pre-script` if it is there, overridable by
// `DOCKER_WRAPPER_PRE_SCRIPT`, and `tests/with-docker+pre-script-test` copies one
// in and asserts the file it creates exists. The native engine ran nothing, so
// that target failed on an absence with no message naming the feature (E925).
func TestThePreScriptIsFoundWhereTheStepPutIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Nothing there is not an error: almost no step has one.
	if at := preScriptIn(root, nil); at != "" {
		t.Errorf("a step with no pre-script named %q", at)
	}

	at := filepath.Join(root, defaultPreScript)

	err := os.MkdirAll(filepath.Dir(at), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(at, []byte("#!/bin/sh\ntrue\n"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	// **Named from inside the step**, because that is the only name that means
	// anything to the shim: it chroots first, so a guest-side path would be
	// looked up in a filesystem the step cannot see.
	if got := preScriptIn(root, nil); got != defaultPreScript {
		t.Errorf("preScriptIn = %q, want %q", got, defaultPreScript)
	}
}

// The environment may move it, which is the half a step controls.
func TestThePreScriptCanBeMoved(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	where := "/opt/setup.sh"

	env := []string{"DOCKER_WRAPPER_PRE_SCRIPT=" + where}

	// Named but absent is not an error either. A step that sets the variable
	// and ships no script is asking for nothing, and refusing would break a
	// build over a file whose absence changes nothing.
	if at := preScriptIn(root, env); at != "" {
		t.Errorf("an absent named script resolved to %q", at)
	}

	err := os.MkdirAll(filepath.Join(root, "opt"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, where), []byte("#!/bin/sh\ntrue\n"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	if got := preScriptIn(root, env); got != where {
		t.Errorf("preScriptIn = %q, want %q", got, where)
	}

	// The default is not consulted once the environment has named one: a step
	// that moved it did so to run *that*, and running both would run something
	// nobody asked for.
	if got := preScriptIn(root, []string{"DOCKER_WRAPPER_PRE_SCRIPT=/nowhere"}); got != "" {
		t.Errorf("a named-but-absent script fell back to %q", got)
	}
}
