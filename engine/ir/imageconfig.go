package ir

import (
	"sort"

	"github.com/EarthBuild/earthbuild/engine/image"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// OCIConfig turns what an Earthfile declared into what an image carries.
//
// One converter, because there were two and they disagreed. The path that saves
// an image to disk and the path that packs one for `docker load` each copied
// these fields by hand, and the second was missing `ExposedPorts` and `Volumes`
// - the two that need converting rather than assigning, because they are sets
// in an OCI configuration and lists here. Everything beside them arrived
// intact, so a `--load`ed image had an entrypoint, a user and no ports, and
// nothing said so (E44).
//
// Here rather than in either caller: `ir` is what both of them already depend
// on, and a converter that lives with the type it converts is one nobody has to
// find twice.
func OCIConfig(c *ImageConfig) ocispec.ImageConfig {
	if c == nil {
		return ocispec.ImageConfig{}
	}

	return ocispec.ImageConfig{
		Entrypoint:   c.Entrypoint,
		Cmd:          c.Cmd,
		Env:          sorted(c.Env),
		WorkingDir:   c.WorkingDir,
		User:         c.User,
		Labels:       c.Labels,
		ExposedPorts: asSet(c.Exposed),
		Volumes:      asSet(c.Volumes),
		StopSignal:   c.StopSignal,
	}
}

// sorted copies an environment into a stable order.
//
// An image's identity is the digest of this configuration, so an unordered
// environment would be a different image on every run from the same input.
func sorted(env []string) []string {
	if len(env) == 0 {
		return nil
	}

	out := append([]string(nil), env...)
	sort.Strings(out)

	return out
}

// asSet turns a list into the set shape an OCI configuration uses.
//
// Nil for an empty list rather than an empty map: an image declaring
// `"Volumes": {}` is making a statement where one declaring nothing is not.
func asSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}

	return out
}

// OCIHealthcheck is the health check an image config declares, in the shape the
// layout writer puts on disk.
//
// Beside OCIConfig rather than inside it, because `ocispec.ImageConfig` has no
// field for one - it is Docker's extension - and the writer carries it as a
// separate value for that reason. Here anyway, with the converter it belongs
// to: **the alternative is each caller reaching into the config itself**, which
// is how the two hand-written copies of E44 started.
func OCIHealthcheck(c *ImageConfig) *image.Healthcheck {
	if c == nil || c.Healthcheck == nil {
		return nil
	}

	return &image.Healthcheck{
		Test:          append([]string(nil), c.Healthcheck.Test...),
		Interval:      c.Healthcheck.Interval,
		Timeout:       c.Healthcheck.Timeout,
		StartPeriod:   c.Healthcheck.StartPeriod,
		StartInterval: c.Healthcheck.StartInterval,
		Retries:       c.Healthcheck.Retries,
	}
}
