//go:build !race

package fleet_test

// underRace is whether this binary was built with the race detector.
//
// The detector does not change what the engine does; it changes how long the
// engine takes, by roughly an order of magnitude. A bound chosen to be *short*
// - to prove a wait is bounded rather than to wait - is the one kind that
// cannot survive that, because it was picked close to the real duration on
// purpose.
const underRace = false
