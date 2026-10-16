package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `STOPSIGNAL` says which signal stops a running container.
//
// Like `HEALTHCHECK`, it changes nothing about the build: no step runs it and
// no filesystem holds it. It is a fact about the *image*, so it belongs in the
// config and the config is in the key.
//
// Stored exactly as written. `9` and `SIGKILL` name the same signal, and docker
// records whichever the author used - so an image built here and one built by
// docker from the same Dockerfile carry the same string, which is the point.
// `EXPOSE` is normalised a few lines away for the opposite reason: there, every
// other tool writes `8080/tcp` and storing `8080` was the odd one out.
func TestAStopSignalIsRecordedOnTheImage(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"a name":           "SIGTERM",
		"another name":     "SIGKILL",
		"a number":         "9",
		"a real-time name": "SIGRTMIN",
		"lower case":       "sigterm",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    STOPSIGNAL "+want+
				"\n    SAVE IMAGE thing:latest\n", testMain)
			if err != nil {
				t.Fatalf("planning STOPSIGNAL %q: %v", want, err)
			}

			if len(p.Images) != 1 {
				t.Fatalf("the plan declares %d images", len(p.Images))
			}

			if got := p.Images[0].Config.StopSignal; got != want {
				t.Errorf("the image stops on %q, want %q", got, want)
			}
		})
	}
}

// An image with a stop signal is a different image.
//
// The config is in an image's identity, so a build that changed only this
// produces a different digest - which is what makes recording it worth anything
// rather than a comment on the side.
func TestAStopSignalChangesTheImage(t *testing.T) {
	t.Parallel()

	const recipe = "\nmain:\n    FROM alpine:3.22\n%s    SAVE IMAGE thing:latest\n"

	with := imageID(t, versioned+fmtRecipe(recipe, "    STOPSIGNAL SIGKILL\n"))
	without := imageID(t, versioned+fmtRecipe(recipe, ""))

	if with == without {
		t.Error("an image with a stop signal keys the same as one without," +
			" so the declaration reaches nothing that matters")
	}

	// And two different signals are two different images, which the check above
	// would pass without: it only says the field is read at all.
	other := imageID(t, versioned+fmtRecipe(recipe, "    STOPSIGNAL SIGTERM\n"))
	if with == other {
		t.Error("SIGKILL and SIGTERM key the same image")
	}
}

// Something that is not a signal is refused, and told why.
//
// The alternative is an image whose config the daemon rejects at `docker run`,
// long after the build that wrote it - and with nothing pointing at the line.
func TestAStopSignalThatIsNotASignalIsRefused(t *testing.T) {
	t.Parallel()

	for name, line := range map[string]string{
		"a word that is not a signal":  "STOPSIGNAL BANANA",
		"a signal that does not exist": "STOPSIGNAL SIGBANANA",
		"out of range":                 "STOPSIGNAL 200",
		"zero":                         "STOPSIGNAL 0",
		"negative":                     "STOPSIGNAL -9",
		"nothing at all":               "STOPSIGNAL",
		"a signal with arithmetic":     "STOPSIGNAL SIGRTMIN+3",
		"two signals":                  "STOPSIGNAL SIGTERM SIGKILL",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    "+line+
				"\n    SAVE IMAGE thing:latest\n", testMain)
			if err == nil {
				t.Fatalf("%q was accepted", line)
			}

			// The line, so the author knows where to look.
			if !strings.Contains(err.Error(), "STOPSIGNAL") {
				t.Errorf("the refusal does not name the command: %v", err)
			}
		})
	}
}
