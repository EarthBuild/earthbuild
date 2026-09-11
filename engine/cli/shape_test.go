package cli

import (
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

// **The shape is the invocation.** What the Earthfiles say is not here: they
// are inputs, recorded by the build that read them. See earthfileDigest.
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
