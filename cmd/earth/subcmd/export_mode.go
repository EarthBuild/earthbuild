package subcmd

import (
	"errors"

	"github.com/EarthBuild/earthbuild/earthfile2llb"
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
