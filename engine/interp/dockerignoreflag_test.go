package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Naming `--use-docker-ignore` does not refuse the file.
//
// **This engine reads `.dockerignore` already, and always.** `engine/ignore`
// looks for `.earthignore`, then `.earthlyignore`, then `.dockerignore` - the
// last "so a project that has one and no Earthfile-specific one gets what it
// plainly meant". The reference engine gates the same behaviour behind this flag
// and only for a Dockerfile's context.
//
// So the flag is a statement about the dialect and not a claim to a feature,
// which is exactly the condition `ignoredFeatures` states for accepting one: it
// enables something this engine implements unconditionally. Accepting it changes
// nothing about what a build does here.
//
// Refusing it cost a working build. `docker-build-integration` writes an
// Earthfile whose VERSION line carries the flag, and the whole file was refused
// at that line for a feature it already had.
func TestNamingTheDockerIgnoreFlagDoesNotRefuseTheFile(t *testing.T) {
	t.Parallel()

	src := "VERSION --use-docker-ignore 0.8\nmain:\n    FROM alpine\n    RUN true\n"

	_, err := interp.Build(src, "main")
	if err != nil {
		t.Errorf("a file naming --use-docker-ignore was refused, although this"+
			" engine reads .dockerignore whether or not it is named: %v", err)
	}
}
