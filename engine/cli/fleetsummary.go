package cli

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// fleetSummary is how much of this build was a fleet, or nothing at all if it
// was not one.
//
// **The negative is the half worth printing.** A fleet that formed, placed
// nothing on its workers and built every step on the driver produces output
// identical to a fleet that delegated everything, and exits zero either way - so
// the one failure that costs real money, a distributed build that quietly is
// not, is the one nothing reports. It is also what a CI job has to assert on:
// prior art on this mechanism checks for the *absence* of local execution
// precisely because a local fallback otherwise passes as success (E505).
//
// The counts, not a verdict: whether 2 of 5 delegated is good depends on the
// graph, on how many workers there were and on what the steps cost, and a tool
// that decided that for the reader would be wrong the first time somebody built
// something with one long pole in it.
func fleetSummary(s fleet.Spend) string {
	if s.Delegated == 0 && s.Local == 0 {
		return ""
	}

	return fmt.Sprintf("  fleet                     %d delegated, %d local\n", s.Delegated, s.Local)
}
