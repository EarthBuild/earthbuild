package subcmd

import (
	"errors"

	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/util/hint"
)

// outputFlags is the raw command-line input that decides how much of a build is
// written out locally. The fields are exactly as the user typed them - nothing
// here is defaulted or overwritten in place, so resolveExport can still tell an
// explicit --no-output apart from one implied by --ci.
type outputFlags struct {
	CI            bool
	Output        bool
	NoOutput      bool
	NoImageOutput bool
	ArtifactMode  bool
	ImageMode     bool
}

// Errors returned by resolveExport, surfaced to the user as parameter errors.
var (
	errNoImageOutputWithImageMode = errors.New(
		"cannot use --no-image-output with image mode")
	errNoOutputWithNoImageOutput = errors.New(
		"cannot use --no-output together with --no-image-output; --no-output already suppresses images")
	errNoOutputWithMode = errors.New(
		"cannot use --no-output with image or artifact modes")
)

// resolveExport turns the raw flags into the single Export a build runs with.
//
// This is the only place output intent is decided. Everything downstream reads
// the resulting Export and never re-derives it from flags, so a new output flag
// is added here once rather than at each site that happens to care.
//
// Contradictory combinations are rejected rather than silently resolved by
// precedence: a flag that quietly loses to another is indistinguishable from a
// flag that does nothing.
func resolveExport(f outputFlags) (earthfile2llb.Export, error) {
	// The image form exists to output an image locally, so suppressing that
	// leaves it with nothing to do.
	if f.ImageMode && f.NoImageOutput {
		return earthfile2llb.ExportAll, errNoImageOutputWithImageMode
	}

	if f.NoOutput && f.NoImageOutput {
		return earthfile2llb.ExportAll, errNoOutputWithNoImageOutput
	}

	if f.NoOutput && (f.ImageMode || f.ArtifactMode) {
		// Outside --ci this is a mistake worth reporting. Under --ci it has long
		// been accepted with the explicit mode winning, and pipelines depend on
		// that, so it stays accepted.
		if !f.CI {
			return earthfile2llb.ExportAll, errNoOutputWithMode
		}

		return earthfile2llb.ExportAll, nil
	}

	if f.NoOutput {
		return earthfile2llb.ExportNone, nil
	}

	if f.NoImageOutput {
		return earthfile2llb.ExportArtifactsOnly, nil
	}

	// Under --ci nothing is written out unless the user asked for output.
	// --no-image-output is such a request: it means "artifacts yes, images no",
	// and is handled above so that it survives this default.
	if f.CI && !f.Output && !f.ArtifactMode && !f.ImageMode {
		return earthfile2llb.ExportNone, nil
	}

	return earthfile2llb.ExportAll, nil
}

// errImageModeWithoutFrontend is returned when image mode is requested on a
// client with no container frontend to load the image into.
var errImageModeWithoutFrontend = hint.Wrap(
	errors.New("cannot use image mode without Docker, Podman or Apple Container to load the image into"),
	"build against the remote buildkitd with --push to publish images instead",
)

// noFrontendImageOutputWarning is printed when local image output is skipped
// because no container frontend is available to load images into.
const noFrontendImageOutputWarning = "No Docker, Podman or Apple Container is available to load images into; " +
	"SAVE IMAGE results will not be output locally. " +
	"Pass --no-image-output to make this explicit (and --push to publish images instead).\n"

// exportWithoutFrontend adjusts export for a client with no container frontend
// (the stub engine), where images cannot be loaded locally. It is decided
// before the build so that an image export does not fail late, mid-build.
//
// Image mode exists only to output an image locally, so it is rejected. The
// default export is downgraded to artifacts only, and skipped reports that it
// was, so the caller can tell the user; pushes are unaffected.
func exportWithoutFrontend(export earthfile2llb.Export, imageMode bool) (
	_ earthfile2llb.Export, skipped bool, _ error,
) {
	if !export.Images() {
		return export, false, nil
	}

	if imageMode {
		return export, false, errImageModeWithoutFrontend
	}

	return earthfile2llb.ExportArtifactsOnly, true, nil
}
