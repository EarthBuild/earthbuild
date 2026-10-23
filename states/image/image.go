// Package image provides data structures and logic for representing and manipulating container images
// within the build state.
package image

import (
	"maps"
	"slices"

	"github.com/EarthBuild/earthbuild/util/llbutil"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Image is a partial of the standard Image struct defined as part of the image opencontainers spec
// at https://github.com/opencontainers/image-spec/blob/master/specs-go/v1/config.go#L82
type Image struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       Config `json:"config"`
}

// NewImage returns a new image.
func NewImage() *Image {
	return &Image{
		Config: Config{
			ImageConfig: specs.ImageConfig{
				ExposedPorts: make(map[string]struct{}),
				Labels:       make(map[string]string),
				Volumes:      make(map[string]struct{}),
				Env:          []string{"PATH=" + llbutil.DefaultPathEnv},
				WorkingDir:   "/",
			},
		},
	}
}

// Clone creates a copy of the image.
func (img *Image) Clone() *Image {
	if img == nil {
		return NewImage()
	}

	clone := &Image{
		Architecture: img.Architecture,
		OS:           img.OS,
		Config: Config{
			ImageConfig: img.Config.ImageConfig,
		},
	}
	clone.Config.Env = slices.Clone(img.Config.Env)
	clone.Config.Entrypoint = slices.Clone(img.Config.Entrypoint)
	clone.Config.Cmd = slices.Clone(img.Config.Cmd)
	clone.Config.ExposedPorts = maps.Clone(img.Config.ExposedPorts)
	clone.Config.Volumes = maps.Clone(img.Config.Volumes)
	clone.Config.Labels = maps.Clone(img.Config.Labels)

	if img.Config.Healthcheck != nil {
		hc := *img.Config.Healthcheck
		hc.Test = slices.Clone(img.Config.Healthcheck.Test)
		clone.Config.Healthcheck = &hc
	}

	return clone
}

// FromBuildKit converts a BuildKit image representation into an EarthBuild Image.
func FromBuildKit(bkImg *dockerspec.DockerOCIImage) *Image {
	if bkImg == nil {
		return nil
	}

	img := &Image{
		Architecture: bkImg.Architecture,
		OS:           bkImg.OS,
		Config: Config{
			ImageConfig: bkImg.Config.ImageConfig,
			Healthcheck: bkImg.Config.Healthcheck,
		},
	}

	return img.Clone()
}

// Config is a docker compatible config for an image.
//
//nolint:embeddedstructfieldcheck // fieldalignment takes precedence over embeddedstructfieldcheck
type Config struct {
	Healthcheck *dockerspec.HealthcheckConfig `json:",omitempty"`
	specs.ImageConfig
}
