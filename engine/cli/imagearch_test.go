package cli

import (
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// An image says what machine it is for, whatever the build was told.
//
// `architecture` and `os` are required by the image specification, and this
// engine wrote both empty whenever the build was not given an explicit
// `--platform`: the platform string was parsed, the parse of "" failed, and the
// error was discarded by an `if err == nil` that left the Spec's zero value in
// place. `docker inspect` reported an empty Architecture on an image that had
// otherwise come out right, and a registry validating its input would refuse it.
//
// Measured against earthly, which reports amd64 on the same machine and the
// same Earthfile. Everything else in the configuration matched exactly - Env in
// order, Cmd, Entrypoint, User, WorkingDir, ExposedPorts, Volumes and the
// author's own labels - which is what made the empty field worth chasing rather
// than one symptom among many.
//
// The silence is the part to keep out. A platform that cannot be parsed is not
// a smaller answer than one that can; it is an image nothing can place.
func TestAnImageAlwaysNamesItsPlatform(t *testing.T) {
	t.Parallel()

	for _, platform := range []string{"", "linux/arm64", "not a platform"} {
		t.Run("platform="+platform, func(t *testing.T) {
			t.Parallel()

			spec := specFor(interp.Image{Ref: "probe:tag"}, platform,
				[]image.LayerSource{}, decl.Declaration{}, time.Time{})

			if spec.Platform.Architecture == "" || spec.Platform.OS == "" {
				t.Errorf("platform %q gave os=%q arch=%q, and an image config"+
					" requires both", platform,
					spec.Platform.OS, spec.Platform.Architecture)
			}
		})
	}

	// A platform that was named is the one used, or the default has replaced
	// the answer instead of standing in for a missing one.
	spec := specFor(interp.Image{Ref: "probe:tag"}, "linux/arm64",
		[]image.LayerSource{}, decl.Declaration{}, time.Time{})
	if spec.Platform.OS != "linux" || spec.Platform.Architecture != "arm64" {
		t.Errorf("an explicit platform became os=%q arch=%q",
			spec.Platform.OS, spec.Platform.Architecture)
	}
}
