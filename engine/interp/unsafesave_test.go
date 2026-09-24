package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A save that leaves the project is refused, whatever the invocation says.
//
// `tests/save-artifact-dont-overwrite.earth` has six targets that exist to be
// refused, and the tree drives them with
// `--version-flag-overrides=require-force-for-unsafe-saves` - a way of turning
// that feature on from outside the file.
//
// **This engine does not need it turned on.** It never writes outside the
// project directory, which is a decision rather than a gap: the refusal is
// stated at three places - here, the CLI, and `insideProject` at the point of
// writing, which resolves symlinks so the position cannot be walked around.
//
// So the flag names a feature this engine always provides, and the targets are
// refused with or without it. That is a claim worth a test rather than a
// comment, because the gate is about to rely on it (E464).
func TestASaveThatLeavesTheProjectIsRefused(t *testing.T) {
	t.Parallel()

	for _, dest := range []string{"/test", "../test", "../other", "/", "/.", "/.."} {
		_, err := interp.Build(versioned+
			"\nmain:\n    FROM alpine:3.22\n    RUN mkdir /data\n"+
			"    SAVE ARTIFACT /data AS LOCAL "+dest+"\n", testMain)
		if err == nil {
			t.Errorf("AS LOCAL %s planned, and it is outside the project", dest)

			continue
		}

		if !strings.Contains(err.Error(), "project") {
			t.Errorf("AS LOCAL %s refused with %q, which does not say why", dest, err)
		}
	}
}

// A save inside the project is ordinary.
//
// The control: a rule that refused every `AS LOCAL` would be a missing feature
// with a safety-shaped explanation.
func TestASaveInsideTheProjectIsFine(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN mkdir /data\n"+
		"    SAVE ARTIFACT /data AS LOCAL out/data\n", testMain)
	if err != nil {
		t.Fatalf("an ordinary AS LOCAL was refused: %v", err)
	}
}
