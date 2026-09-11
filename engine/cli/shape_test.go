package cli

import (
	"strings"
	"testing"
)

const shapeSrc = "VERSION 0.8\n\nbuild:\n    FROM alpine@sha256:aa\n    COPY src.txt /\n    RUN make\n"

func shapeWith(t *testing.T, edit func(*shapeInput)) string {
	t.Helper()

	in := shapeInput{
		Source: []byte(shapeSrc), Target: "build", Platform: "linux/arm64",
		Args: map[string]string{"A": "1"},
	}

	if edit != nil {
		edit(&in)
	}

	got, err := shapeOf(in)
	if err != nil {
		t.Fatalf("shape: %v", err)
	}

	return got.String()
}

// **The shape is the build, and the build is the file plus the invocation.**
// No plan, no graph, no interpretation: an Earthfile, what was asked of it, and
// the values handed in determine every step there will be.
func TestTheShapeIsStableForTheSameBuild(t *testing.T) {
	t.Parallel()

	first := shapeWith(t, nil)

	second := shapeWith(t, nil)
	if first != second {
		t.Error("the same build gave two shapes")
	}
}

// Each of these changes what the build does, and each must move it.
func TestEverythingThatChangesTheBuildMovesTheShape(t *testing.T) {
	t.Parallel()

	was := shapeWith(t, nil)

	for what, edit := range map[string]func(*shapeInput){
		"an edited command": func(in *shapeInput) {
			in.Source = []byte(shapeSrc + "    RUN make install\n")
		},
		"a moved base image": func(in *shapeInput) {
			in.Source = []byte(strings.ReplaceAll(shapeSrc, "sha256:aa", "sha256:bb"))
		},
		"a different target":   func(in *shapeInput) { in.Target = "test" },
		"a different platform": func(in *shapeInput) { in.Platform = "linux/amd64" },
		"a changed build arg":  func(in *shapeInput) { in.Args = map[string]string{"A": "2"} },
		"an added build arg": func(in *shapeInput) {
			in.Args = map[string]string{"A": "1", "B": "1"}
		},
		"a changed secret": func(in *shapeInput) {
			in.SecretDigests = map[string]string{"S": "a-keyed-digest"}
		},
		"--push":   func(in *shapeInput) { in.Push = true },
		"--strict": func(in *shapeInput) { in.Strict = true },
		"a version flag": func(in *shapeInput) {
			in.VersionFlags = []string{"--sync"}
		},
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			if shapeWith(t, edit) == was {
				t.Errorf("%s left the shape equal", what)
			}
		})
	}
}

// A comment is not a command. Hashing the file's bytes would rebuild on a
// reformat, which is the coarseness that makes people turn a cache off.
func TestAReformattedEarthfileDoesNotMoveTheShape(t *testing.T) {
	t.Parallel()

	was := shapeWith(t, nil)

	// **In the middle, not at the end.** A trailing comment shifts nothing, so
	// a test that appends one passes whether or not source locations are
	// stripped - which is what this test was doing until the stripping was
	// removed and it went on passing.
	for what, src := range map[string]string{
		"a comment above a command": strings.Replace(shapeSrc,
			"    RUN make", "    # why\n    RUN make", 1),
		"a blank line": strings.Replace(shapeSrc,
			"    COPY src.txt /", "\n    COPY src.txt /", 1),
		"a trailing comment": shapeSrc + "\n# a comment\n",
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			got := shapeWith(t, func(in *shapeInput) { in.Source = []byte(src) })
			if got != was {
				t.Errorf("%s moved the shape", what)
			}
		})
	}
}

// **A build spread over more than one Earthfile cannot be keyed by one of
// them.** `./sub+target` and `IMPORT` bring in a file this does not hash, so a
// change there would be invisible - which is a skip on a build that changed.
// Refused until those are hashed too.
func TestABuildReachingAnotherEarthfileHasNoShape(t *testing.T) {
	t.Parallel()

	for what, src := range map[string]string{
		"a local target reference": "VERSION 0.8\n\nbuild:\n    COPY ./sub+thing/x /\n",
		"an import":                "VERSION 0.8\nIMPORT ./sub AS sub\n\nbuild:\n    FROM sub+base\n",
		"a computed reference":     "VERSION 0.8\n\nbuild:\n    BUILD ./$DIR+thing\n",
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			_, err := shapeOf(shapeInput{Source: []byte(src), Target: "build"})
			if err == nil {
				t.Errorf("%s produced a shape", what)
			}
		})
	}
}

// An unpinned reference is the other way a file does not determine the build.
func TestAnUnpinnedReferenceHasNoShape(t *testing.T) {
	t.Parallel()

	_, err := shapeOf(shapeInput{
		Source: []byte("VERSION 0.8\n\nbuild:\n    FROM alpine:3.22\n"), Target: "build",
	})
	if err == nil {
		t.Error("an unpinned base image produced a shape")
	}
}

// **With no key configured the shape covers which secrets a build carries, not
// what they are.** A weaker claim than the digest, made deliberately: without a
// key there is nothing to fold a value into, and refusing outright would leave
// `--auto-skip` doing nothing for anyone who has not configured an HMAC.
//
// What it costs is written down as a test rather than as a sentence: a rotated
// credential does not move the shape.
func TestWithNoKeyTheShapeCoversTheSecretsNames(t *testing.T) {
	t.Parallel()

	const usesASecret = "VERSION 0.8\n\nbuild:\n    FROM alpine@sha256:aa\n" +
		"    RUN --secret TOKEN echo hi\n"

	named := func(names ...string) string {
		t.Helper()

		got, err := shapeOf(shapeInput{
			Source: []byte(usesASecret), Target: "build", SecretNames: names,
		})
		if err != nil {
			t.Fatalf("a build reading a secret with no key was refused: %v", err)
		}

		return got.String()
	}

	if named("TOKEN") == named("TOKEN", "OTHER") {
		t.Error("a secret added did not move the shape")
	}

	first := named("TOKEN")

	again := named("TOKEN")
	if first != again {
		t.Error("the same secrets gave two shapes")
	}

	// The cost, stated: this is what configuring a key buys.
	if !secretsAreKeyedByName(shapeInput{Source: []byte(usesASecret)}) {
		t.Error("a build reading a secret with no key does not say it is keyed by name")
	}
}

// **The two key spaces cannot meet.** A record made before a key was configured
// must not be compared against one made after: the first covers names and the
// second covers values, and finding the first and trusting it would be skipping
// on the weaker claim while believing the stronger.
func TestANameKeyedShapeIsNotADigestKeyedOne(t *testing.T) {
	t.Parallel()

	const src = "VERSION 0.8\n\nbuild:\n    FROM alpine@sha256:aa\n" +
		"    RUN --secret TOKEN echo hi\n"

	byName, err := shapeOf(shapeInput{
		Source: []byte(src), Target: "build", SecretNames: []string{"TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}

	byDigest, err := shapeOf(shapeInput{
		Source: []byte(src), Target: "build",
		SecretDigests: map[string]string{"TOKEN": "a-keyed-digest"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if byName == byDigest {
		t.Error("a shape keyed on secret names equals one keyed on their values")
	}
}

// A build carrying no secrets at all is not keyed by name, and says so.
func TestABuildWithNoSecretsIsNotKeyedByName(t *testing.T) {
	t.Parallel()

	if secretsAreKeyedByName(shapeInput{Source: []byte(shapeSrc)}) {
		t.Error("a build reading no secrets claims to be keyed by secret name")
	}
}
