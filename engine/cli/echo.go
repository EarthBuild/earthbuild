package cli

import (
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// echoer is an executor that can replay what a step printed.
//
// Asserted rather than required, as every other optional capability here is: an
// executor that cannot replay keeps its steps' output to itself, and a build
// over it is silent on a hit exactly as every build was before this existed.
type echoer interface {
	Echo(n *ir.Node, out string)
}

// echoOf is how a scheduler replays a served step's output, or nil.
//
// **Through the executor, because that is where the sink is.** A step's lines
// reach a progress display and a `$( )` substitution by one path, and a hit has
// to use the same one or the two disagree about what the step said.
func echoOf(x core.Executor) func(*ir.Node, string) {
	e, ok := x.(echoer)
	if !ok {
		return nil
	}

	return e.Echo
}
