package domain

import (
	"fmt"
)

var _ Reference = DockerfileTarget{}

// DockerfileTarget represents a reference to a target in a Dockerfile.
type DockerfileTarget struct {
	// SourcePath is the file path to the Dockerfile (e.g. "Dockerfile" or "./Dockerfile").
	SourcePath string `json:"sourcePath"`
	// Target is the target name (e.g. "build", "stage-1", "runner").
	Target string `json:"target"`
}

// GetGitURL returns empty string for DockerfileTarget.
func (dt DockerfileTarget) GetGitURL() string { return "" }

// GetTag returns empty string for DockerfileTarget.
func (dt DockerfileTarget) GetTag() string { return "" }

// GetLocalPath returns empty string for DockerfileTarget.
func (dt DockerfileTarget) GetLocalPath() string { return "" }

// GetSourcePath returns the Dockerfile path.
func (dt DockerfileTarget) GetSourcePath() string { return dt.SourcePath }

// GetImportRef returns empty string for DockerfileTarget.
func (dt DockerfileTarget) GetImportRef() string { return "" }

// GetName returns the target name.
func (dt DockerfileTarget) GetName() string { return dt.Target }

// IsExternal returns false for DockerfileTarget.
func (dt DockerfileTarget) IsExternal() bool { return false }

// IsLocalInternal returns false for DockerfileTarget.
func (dt DockerfileTarget) IsLocalInternal() bool { return false }

// IsLocalExternal returns false for DockerfileTarget.
func (dt DockerfileTarget) IsLocalExternal() bool { return false }

// IsRemote returns false for DockerfileTarget.
func (dt DockerfileTarget) IsRemote() bool { return false }

// IsImportReference returns false for DockerfileTarget.
func (dt DockerfileTarget) IsImportReference() bool { return false }

// IsUnresolvedImportReference returns false for DockerfileTarget.
func (dt DockerfileTarget) IsUnresolvedImportReference() bool { return false }

// IsDefaultTarget returns whether this target is the default entrypoint target ("build").
func (dt DockerfileTarget) IsDefaultTarget() bool {
	return dt.Target == "build"
}

// DebugString returns a string for debugging.
func (dt DockerfileTarget) DebugString() string {
	return fmt.Sprintf("SourcePath: %q; Target: %q", dt.SourcePath, dt.Target)
}

// String returns the string representation of the DockerfileTarget.
func (dt DockerfileTarget) String() string {
	if dt.IsDefaultTarget() {
		return escapePlus(dt.SourcePath)
	}

	return fmt.Sprintf("%s+%s", escapePlus(dt.SourcePath), dt.Target)
}

// StringCanonical returns canonical string representation.
func (dt DockerfileTarget) StringCanonical() string {
	return dt.String()
}

// ProjectCanonical returns project canonical representation.
func (dt DockerfileTarget) ProjectCanonical() string {
	return escapePlus(dt.SourcePath)
}
