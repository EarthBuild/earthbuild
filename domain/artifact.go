package domain

import (
	"fmt"
	"path"
	"strings"
)

// Artifact is an earth artifact identifier.
type Artifact struct {
	Target   Target
	Artifact string
}

// Clone returns a copy of the Artifact.
func (a Artifact) Clone() Artifact {
	return a
}

// String returns a string representation of the Artifact.
func (a Artifact) String() string {
	return fmt.Sprintf("%s%s", a.Target.String(), path.Join("/", escapePlus(a.Artifact)))
}

// StringCanonical returns a string representation of the Artifact.
func (a Artifact) StringCanonical() string {
	return fmt.Sprintf("%s%s", a.Target.StringCanonical(), path.Join("/", escapePlus(a.Artifact)))
}

// ParseArtifact parses a string representation of an Artifact.
func ParseArtifact(artifactName string) (Artifact, error) {
	parts, err := splitUnescapePlus(artifactName)
	if err != nil {
		return Artifact{}, err
	}

	if len(parts) != 2 {
		return Artifact{}, fmt.Errorf("invalid artifact name %s", artifactName)
	}

	partsSlash := strings.SplitN(parts[1], "/", 2)
	if len(partsSlash) != 2 {
		return Artifact{}, fmt.Errorf("invalid artifact name %s", artifactName)
	}

	earthTargetName := escapePlus(parts[0]) + "+" + partsSlash[0]

	target, err := ParseTarget(earthTargetName)
	if err != nil {
		return Artifact{}, fmt.Errorf("invalid artifact name %s: %w", artifactName, err)
	}

	artifactPath := "/" + partsSlash[1]

	return Artifact{
		Target:   target,
		Artifact: artifactPath,
	}, nil
}
