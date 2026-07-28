package llbutil

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/distribution/reference"
)

// PlatformSpecificImageName returns the PlatformSpecificImageName.
func PlatformSpecificImageName(imgName string, platform platutil.Platform) (string, error) {
	platformStr := platform.String()
	if platformStr == "" {
		platformStr = "native"
	}

	r, err := reference.ParseNormalizedNamed(imgName)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", imgName, err)
	}

	taggedR, ok := reference.TagNameOnly(r).(reference.Tagged)
	if !ok {
		return "", fmt.Errorf("not tagged %s: %w", reference.TagNameOnly(r).String(), err)
	}

	platformTag := DockerTagSafe(fmt.Sprintf("%s_%s", taggedR.Tag(), platformStr))

	r2, err := reference.WithTag(r, platformTag)
	if err != nil {
		return "", fmt.Errorf("with tag %s - %s: %w", r.String(), platformTag, err)
	}

	return reference.FamiliarString(r2), nil
}
