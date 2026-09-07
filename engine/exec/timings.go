package exec

import (
	"github.com/EarthBuild/earthbuild/engine/timing"
)

// EnvTimings makes a build say where its time went. See package timing.
const EnvTimings = timing.Env

// phase times one phase of one step. The phases here are the round trips this
// engine makes into the sandbox, so timing them from out here measures the
// guest without instrumenting it - which is how materialise was found to be the
// whole of the per-step cost (E528).
func phase(name, where string) func() { return timing.Phase(name, where) }
