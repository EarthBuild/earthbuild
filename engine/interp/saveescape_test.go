package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `SAVE ARTIFACT ... AS LOCAL` writes to the machine running the build, and
// where it writes comes from the Earthfile.
//
// Since a remote reference makes this build interpret an Earthfile fetched from
// somewhere else, a destination that escapes the project directory is a remote
// repository writing anywhere it likes on this machine - a crontab, an SSH
// authorized_keys, a shell profile. The destination is a place inside the
// project or it is refused.
func TestAnArtifactCannotBeSavedOutsideTheProject(t *testing.T) {
	t.Parallel()

	for _, dest := range []string{
		"../escaped.txt",
		"../../etc/cron.d/evil",
		"sub/../../escaped.txt",
		"/etc/passwd",
		"/tmp/pwned",
	} {
		t.Run(dest, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    SAVE ARTIFACT /x AS LOCAL "+dest+"\n", testMain)
			if err == nil {
				t.Fatalf("%q was accepted as a place to write", dest)
			}

			if !strings.Contains(err.Error(), "project") {
				t.Errorf("the refusal does not say what is wrong:\n%s", err)
			}
		})
	}
}

// An ordinary destination still works, including one in a subdirectory.
func TestAnArtifactIsSavedWhereItSays(t *testing.T) {
	t.Parallel()

	for _, dest := range []string{"out.txt", "./out.txt", "build/out.txt", "a/b/c.txt"} {
		t.Run(dest, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    SAVE ARTIFACT /x AS LOCAL "+dest+"\n", testMain)
			if err != nil {
				t.Fatal(err)
			}

			if len(p.Artifacts) != 1 || p.Artifacts[0].LocalDest == "" {
				t.Fatalf("the artifact was dropped: %+v", p.Artifacts)
			}
		})
	}
}
