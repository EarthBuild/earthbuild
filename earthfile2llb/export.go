package earthfile2llb

import "fmt"

// Export describes how much of a build's output is written out locally.
type Export int

const (
	// ExportAll writes out both SAVE IMAGE images and SAVE ARTIFACT ... AS LOCAL
	// artifacts. This is the default.
	ExportAll Export = iota
	// ExportArtifactsOnly writes out SAVE ARTIFACT ... AS LOCAL artifacts, but does
	// not load SAVE IMAGE images into the local container engine (--no-image-output).
	// Pushes are unaffected, so a build can push images and still write artifacts.
	ExportArtifactsOnly
	// ExportNone writes out neither images nor artifacts (--no-output).
	ExportNone
)

// Images reports whether SAVE IMAGE images should be loaded into the local
// container engine.
//
// Prefer this over comparing against a constant: it names the question being
// asked, so a site that cares about images cannot accidentally be written with
// the test for artifacts.
func (e Export) Images() bool {
	return e == ExportAll
}

// Artifacts reports whether SAVE ARTIFACT ... AS LOCAL artifacts should be
// written to the local filesystem.
func (e Export) Artifacts() bool {
	return e != ExportNone
}

// String returns the human-readable description of the export mode.
func (e Export) String() string {
	switch e {
	case ExportAll:
		return "all"
	case ExportArtifactsOnly:
		return "artifacts-only"
	case ExportNone:
		return "none"
	default:
		return fmt.Sprintf("Export(%d)", int(e))
	}
}
