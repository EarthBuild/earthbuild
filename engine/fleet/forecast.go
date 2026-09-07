package fleet

import (
	"strconv"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Step is one unit of work as a forecast sees it: what it needs and what it
// leaves behind.
type Step struct {
	// Base and Sources are the layers it reads.
	Base    []ir.NodeID
	Sources []ir.NodeID
	// Produces is the layer it leaves on whichever machine ran it.
	Produces ir.NodeID
	// Size is how big that layer is, in bytes.
	Size int64
}

// Forecast is what a build would cost a fleet, before running it.
type Forecast struct {
	// Delegated is how many steps went to a worker.
	Delegated int
	// Transfers and Moved are how often, and how much, a layer had to cross
	// between machines.
	Transfers int
	Moved     int64
	// FromOrigin is the part of Moved that came from outside the fleet - a seed
	// base, an image - rather than from another worker.
	//
	// Kept apart because the two have different remedies: bytes between workers
	// come down by placing a step where its inputs already are, bytes from the
	// origin by not delegating the step at all. On a cold fleet this is the
	// larger number, and it was the one the model omitted entirely (E315).
	FromOrigin int64
	// Ran is how many steps each machine took.
	Ran map[string]int
}

// Predict says what a fleet would have to move to build these steps.
//
// **The point is that it is the same code.** `preferFree` decides here exactly as
// it decides in `Assign`, so a change to placement changes the forecast without
// anybody remembering to update it. A simulator with its own model of placement
// agrees with the engine until somebody edits one of them, and after that its
// agreement is worth nothing - it is two guesses checking each other.
//
// What it can answer is everything that is a function of the graph and the
// fleet: which machine runs what, and what has to cross to make that possible.
// What it cannot answer is everything that only exists in the plural or in
// time - a worker's single uplink, a transport timeout, two steps fetching the
// same base at once. Those need real machines, and every one of this project's
// findings in that class was invisible to a model (E266, E256, E215).
//
// concurrent is how many steps are in flight at once, which is what makes a
// holder busy and therefore not the cheapest place for the next step.
func Predict(steps []Step, workers, concurrent int) Forecast {
	return PredictWith(steps, workers, concurrent, nil)
}

// PredictWith is Predict, told how big the layers the build starts from are.
//
// A layer no step produces - the seed base, an image pulled from a registry -
// has no `Step` to carry its size, so without this the model knows a transfer
// happened and not how large it was.
//
// Optional, and a size it is not given is a transfer counted with no bytes
// against it. That is the honest shape: the count is a property of the graph
// and the bytes are a property of the inputs, and pretending the second is zero
// because nobody said is how the model came to report nothing for a run that
// moved 1.6 MiB (E315).
func PredictWith(
	steps []Step, workers, concurrent int, sizes map[ir.NodeID]int64,
) Forecast {
	return PredictAt(steps, workers, concurrent, sizes, nil)
}

// PredictAt is PredictWith at a stated rate, which is what prices a fetch
// against a step.
//
// **The model has to price as the engine prices.** `Predict` exists because it
// is the engine's own placement rather than a second model of it, and a fetch
// charged at a constant here while the driver charges by size (E317) would make
// the two disagree exactly when the base is large - which is the case the whole
// question is about.
//
// A nil rate is the constant, which is what an engine that has measured nothing
// also uses.
func PredictAt(
	steps []Step, workers, concurrent int, sizes map[ir.NodeID]int64, rate *Rate,
) Forecast {
	if rate == nil {
		rate = &Rate{}
	}

	if workers < 1 {
		workers = 1
	}

	if concurrent < 1 {
		concurrent = 1
	}

	fleet := make([]joined, 0, workers)
	holds := make([]map[ir.NodeID]bool, workers)

	for i := range workers {
		at := "fleet-" + strconv.Itoa(i)
		fleet = append(fleet, joined{id: at, at: at})
		holds[i] = map[ir.NodeID]bool{}
	}

	out := Forecast{Ran: map[string]int{}}
	busy := map[string]int{}

	// Where each layer was produced, which is what the driver knows and what it
	// forwards as a holder hint (E260).
	at := map[ir.NodeID]string{}

	for i, s := range steps {
		// The wave this step is in. Steps in one wave overlap, so each sees the
		// others as load; a new wave starts from an idle fleet.
		if concurrent > 0 && i%concurrent == 0 {
			busy = map[string]int{}
		}

		// Priced by what this step would have to pull to run somewhere new,
		// which is every input the cheapest machine might lack. The engine
		// prices the same quantity from the same function (E317).
		order := preferFetching(fleet, holdersOf(s, at), busy,
			rate.Slots(inputBytes(steps, sizes, s)))
		w := order[0]

		idx := 0

		for j := range fleet {
			if fleet[j].id == w.id {
				idx = j
			}
		}

		for _, id := range inputsOf(s) {
			// Everything this machine does not already hold, wherever it comes
			// from.
			//
			// **A base from the driver used to be excluded** on the argument
			// that it is not a cost placement can do anything about. It is: the
			// step can run where the bytes already are, or not be delegated at
			// all - and on a cold fleet it is the *largest* cost there is,
			// because every worker's first step pulls a base nobody else has.
			//
			// The model reported zero bytes for a run that moved 1.6 MiB (E315)
			// and a scheduler tuned against that number would place work
			// precisely where the bytes are worst.
			if holds[idx][id] {
				continue
			}

			n := sizeOf(steps, sizes, id)

			out.Transfers++
			out.Moved += n

			if at[id] == "" {
				out.FromOrigin += n
			}

			holds[idx][id] = true
		}

		holds[idx][s.Produces] = true
		at[s.Produces] = w.at
		busy[w.id]++
		out.Ran[w.id]++
		out.Delegated++
	}

	return out
}

// holdersOf is where this step's inputs are, as the driver would say.
func holdersOf(s Step, at map[ir.NodeID]string) []string {
	var out []string

	seen := map[string]bool{}

	for _, id := range inputsOf(s) {
		if a := at[id]; a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}

	return out
}

// inputsOf is base first, then sources, which is the order the driver names
// holders in - so the biggest thing that would otherwise move is preferred
// first.
func inputsOf(s Step) []ir.NodeID {
	out := make([]ir.NodeID, 0, len(s.Base)+len(s.Sources))
	out = append(out, s.Base...)

	return append(out, s.Sources...)
}

// sizeOf is how big a layer is, according to the step that produced it.
func sizeOf(steps []Step, sizes map[ir.NodeID]int64, id ir.NodeID) int64 {
	for _, s := range steps {
		if s.Produces == id {
			return s.Size
		}
	}

	return sizes[id]
}

// inputBytes is what one step stands on, in bytes, or zero if any of it is
// unknown.
//
// The same all-or-nothing rule the driver uses: a partial sum reads as a full
// price, and under-pricing a base is how a fleet talks itself into shipping
// something it should not have.
func inputBytes(steps []Step, sizes map[ir.NodeID]int64, s Step) int64 {
	var out int64

	for _, id := range inputsOf(s) {
		n := sizeOf(steps, sizes, id)
		if n <= 0 {
			return 0
		}

		out += n
	}

	return out
}
