package flag

import (
	"github.com/EarthBuild/earthbuild/internal/env"
	"github.com/urfave/cli/v3"
)

// EarthEnvVars returns a value source chain for the given env var suffix that
// accepts both the current EARTH_ prefix and the deprecated EARTHLY_ prefix,
// with EARTH_ taking precedence.
//
// NOTE: the EARTHLY_ fallback is a temporary shim to support the
// EARTHLY_ -> EARTH_ migration. Once EARTHLY_ support is officially dropped,
// this helper can return cli.EnvVars(env.Prefix + suffix) (or be
// removed entirely in favour of cli.EnvVars).
func EarthEnvVars(suffix string) cli.ValueSourceChain {
	return cli.EnvVars(env.Prefix+suffix, env.DeprecatedPrefix+suffix)
}
