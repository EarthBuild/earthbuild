// Command earth-diff builds one target under both engines and says whether they
// agree.
//
// **The question the parity ratchet cannot answer.** That gate counts how many
// of the tree's invocations survive being lifted out of the recipe that prepares
// them, which is a fact about the harness as much as about the engine. Asking
// both engines the same question in the same directory is a fact about the
// engines alone (E882c).
//
// Exit codes only, deliberately. Comparing output needs normalisation - paths,
// digests, timings, ordering - which is most of the cost the test plan puts on
// this tool, and none of it is needed to answer "does this engine refuse
// something the reference builds". A tool that answers one question today is
// worth more than one that would answer three next quarter.
package main

// Verdict is what a pair of exit codes says about the two engines.
type Verdict int

// The verdicts. Only two of them are interesting, and they are interesting in
// opposite directions.
const (
	// Agree means both engines reached the same kind of answer. Two different
	// non-zero codes still agree: the build failed either way, and *why* it
	// failed is the build's business rather than a difference between engines.
	Agree Verdict = iota
	// NativeGap is this engine refusing or failing what the reference builds.
	// The only verdict that is a defect here.
	NativeGap
	// NativeAhead is this engine building what the reference cannot. Worth
	// reporting because no parity number can show it: the ratchet only counts
	// what this engine fails to do.
	NativeAhead
)

func (v Verdict) String() string {
	switch v {
	case NativeGap:
		return "native-gap"
	case NativeAhead:
		return "native-ahead"
	case Agree:
		return "agree"
	default:
		return "unknown"
	}
}

// Compare reduces two exit codes to a verdict.
func Compare(native, buildkit int) Verdict {
	switch {
	case native != 0 && buildkit == 0:
		return NativeGap
	case native == 0 && buildkit != 0:
		return NativeAhead
	default:
		return Agree
	}
}
