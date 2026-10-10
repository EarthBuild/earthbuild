package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEarthfile(t *testing.T, at, body string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(at), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(at, []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

const anEarthfile = "VERSION 0.8\n\nbuild:\n    FROM alpine@sha256:aa\n    RUN make\n"

// **An Earthfile is an input, and its digest is what it means.**
//
// This is what replaced a shape that hashed one file and a set of refusals for
// every way a build could involve more than one. A change to any Earthfile a
// build read moves the key, whichever file it was; a comment does not.
func TestAnEarthfilesDigestIsWhatItMeans(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "Earthfile")
	writeEarthfile(t, at, anEarthfile)

	was := earthfileDigest(at)

	for what, body := range map[string]string{
		"an edited command":  anEarthfile + "    RUN make install\n",
		"a moved base image": "VERSION 0.8\n\nbuild:\n    FROM alpine@sha256:bb\n    RUN make\n",
		"a new target":       anEarthfile + "\nother:\n    FROM scratch\n",
	} {
		t.Run("moves: "+what, func(t *testing.T) {
			t.Parallel()

			at := filepath.Join(t.TempDir(), "Earthfile")
			writeEarthfile(t, at, body)

			if earthfileDigest(at) == was {
				t.Errorf("%s did not move the digest", what)
			}
		})
	}

	for what, body := range map[string]string{
		"a comment above a command": "VERSION 0.8\n\nbuild:\n    FROM alpine@sha256:aa\n" +
			"    # why\n    RUN make\n",
		"a blank line": "VERSION 0.8\n\n\nbuild:\n    FROM alpine@sha256:aa\n    RUN make\n",
	} {
		t.Run("does not move: "+what, func(t *testing.T) {
			t.Parallel()

			at := filepath.Join(t.TempDir(), "Earthfile")
			writeEarthfile(t, at, body)

			if earthfileDigest(at) != was {
				t.Errorf("%s moved the digest", what)
			}
		})
	}
}

// An Earthfile that is gone, or that stopped parsing, is a difference - never
// an answer equal to what it was.
func TestAnUnreadableEarthfileIsADifference(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	at := filepath.Join(dir, "Earthfile")
	writeEarthfile(t, at, anEarthfile)

	was := earthfileDigest(at)

	writeEarthfile(t, at, "VERSION 0.8\n\nbuild:\n    FROM\x00 nonsense\n  bad indent\n")

	if earthfileDigest(at) == was {
		t.Error("an Earthfile that stopped parsing kept its digest")
	}

	if earthfileDigest(filepath.Join(dir, "nowhere", "Earthfile")) == was {
		t.Error("an Earthfile that is not there kept its digest")
	}
}

// **Every kind of input must be one `now` can re-read.**
//
// A kind it does not know answers with something no digest equals, so the
// record never holds and the build never skips - silently, and for every build,
// because one unreadable input poisons the whole key. That is what happened
// when `earthfile` was added as a kind and the re-reading switch was edited in
// the wrong file: the mechanism was dead and every test still passed, because
// no test compared a record containing one against a checkout.
func TestEveryInputKindCanBeReRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	at := filepath.Join(dir, "Earthfile")
	writeEarthfile(t, at, anEarthfile)

	err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{inputFile, inputListing, inputAbsent, inputEarthfile} {
		path := "a.txt"
		if kind == inputEarthfile {
			path = at
		}

		if kind == inputListing {
			path = "."
		}

		got := hostInput{Path: path, Kind: kind}.now(dir)
		if strings.HasPrefix(got, "unknown kind") {
			t.Errorf("%s is a kind this code produces and cannot re-read", kind)
		}

		if got == "" {
			t.Errorf("%s re-read to nothing", kind)
		}
	}
}
