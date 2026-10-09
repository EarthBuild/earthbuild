package earthfile2llb

import (
	"github.com/EarthBuild/earthbuild/states"
)

// ImagePlanOpt holds the build-wide options that decide what builder.go does
// with a SAVE IMAGE. builder.go sets it once, on the root ConvertOpt, and every
// target inherits it unchanged, so the converter can ask PlanImage the same
// question builder.go later acts on.
type ImagePlanOpt struct {
	// Export is the user's intent for the whole build (--no-output,
	// --no-image-output).
	Export Export
	// Push is whether the build runs with --push.
	Push bool
	// OnlyArtifact is whether the build only outputs a single artifact
	// (--artifact).
	OnlyArtifact bool
	// OnlyFinalTargetImages is whether only the final target's images are
	// output (--image).
	OnlyFinalTargetImages bool
}

// ImagePlan is what builder.go does with one SAVE IMAGE: whether it is loaded
// into the local container engine, and whether it is pushed to its registry.
type ImagePlan struct {
	// Export is whether the image is loaded into the local container engine.
	Export bool
	// Push is whether the image is pushed to its registry.
	Push bool
}

// PlanImage decides what builder.go does with one SAVE IMAGE. isFinal is whether
// sts is the build's root target.
//
// This is the only place that decision is made. builder.go acts on it, the
// end-of-build summary reports on it, and the converter asks it whether
// builder.go is going to solve a state anyway. None of them can disagree about
// an export or a push, which they could when each recomputed the conditions.
func PlanImage(opt ImagePlanOpt, sts *states.SingleTarget, isFinal bool, saveImage states.SaveImage) ImagePlan {
	// An untagged image has no name to be loaded or pushed under.
	tagged := saveImage.DockerTag != ""
	doSave := sts.GetDoSaves() || saveImage.ForceSave

	return ImagePlan{
		Export: tagged &&
			doSave &&
			opt.Export.Images() &&
			!opt.OnlyArtifact &&
			(!opt.OnlyFinalTargetImages || isFinal),
		Push: tagged &&
			opt.Push &&
			saveImage.Push &&
			!sts.Target.IsRemote() &&
			sts.GetDoPushes(),
	}
}

// SolvedByBuilder reports whether builder.go solves saveImage's state when it
// acts on this plan. cacheExport is whether the build exports a cache
// (--remote-cache), which makes builder.go solve SAVE IMAGE --cache-hint images
// too.
func (p ImagePlan) SolvedByBuilder(saveImage states.SaveImage, cacheExport bool) bool {
	if saveImage.BuilderSkips() {
		// Exported by a wait block instead.
		return false
	}

	if !p.Push && saveImage.HasPushDependencies {
		return false
	}

	return p.Push || p.Export || (saveImage.CacheHint && cacheExport)
}
