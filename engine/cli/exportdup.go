package cli

import "github.com/EarthBuild/earthbuild/engine/ir"

// scheduled reports whether a node is one the graph will run.
//
// **A target read twice is two sets of nodes and one graph.** Interpretation is
// memoised on a target's name, platform and arguments, so one target reached
// with different arguments is read more than once, and each reading appends its
// `SAVE ARTIFACT ... AS LOCAL` and `SAVE IMAGE` to the plan. Only the readings
// the graph reaches are ever scheduled; the rest name nodes that exist as
// objects and are not in the build.
//
// This repository's own Earthfile does it three times for `+earthly`. The export
// loop stopped at the first unscheduled one and reported that the step producing
// it had not run - while another reading had written exactly that file, and the
// reference engine builds the same target without complaint (E895).
//
// **The distinction this restores is between "never asked to run" and "ran and
// produced nothing".** Both leave an empty stack, and only the second is worth
// reporting: a node in the graph that produced no layers is a build that
// promised an output and did not write it, which is the case the check was
// written for.
func scheduled(g *ir.Graph) map[ir.NodeID]bool {
	if g == nil {
		return nil
	}

	in := map[ir.NodeID]bool{}
	for _, n := range g.Nodes() {
		in[n.ID()] = true
	}

	return in
}
