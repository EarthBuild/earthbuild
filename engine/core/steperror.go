package core

import (
	"fmt"
	"strings"
)

// StepError reports a step that ran and failed.
//
// Distinct from an executor error, which means the step could not be run at all.
// The distinction matters to the caller: a failed command is the user's problem
// and names a line in their Earthfile, while a broken sandbox is ours.
type StepError struct {
	Source string // where in the Earthfile
	Desc   string // the command, as written
	Exit   int
	Output string
	// Streamed says the output has already been shown to whoever is watching.
	//
	// The error then names the failure and points at it rather than printing it
	// again: a build that streams and then repeats produces every failing
	// command's output twice, with the second copy truncated at the guest's cap.
	// It is what made a `grep -c` over `+lint` count most findings twice and
	// report a total that moved for reasons unrelated to the code (E73).
	Streamed bool
}

func (e *StepError) Error() string {
	var b strings.Builder

	if e.Desc != "" {
		fmt.Fprintf(&b, "%s", e.Desc)
	} else {
		b.WriteString("step")
	}

	fmt.Fprintf(&b, " failed with exit code %d", e.Exit)

	if e.Source != "" {
		fmt.Fprintf(&b, " (%s)", e.Source)
	}

	// The output is the whole point when nobody has seen it: an exit code alone
	// sends the reader back to run the command by hand to find out what it said.
	// When it has already been streamed, saying where it went beats saying it
	// twice.
	if e.Streamed {
		b.WriteString("\n  its output is above")
	}

	if out := strings.TrimSpace(e.Output); out != "" && !e.Streamed {
		b.WriteString("\n")

		for line := range strings.SplitSeq(out, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	// 127 has one cause and it is worth naming: the shell could not find the
	// command. Generic codes get no such hint, because a guess that is usually
	// wrong is worse than silence.
	if e.Exit == 127 {
		b.WriteString("  exit 127 means the command was not found in the image")
	}

	return strings.TrimRight(b.String(), "\n")
}

// ToleratedFailureError is a step that failed without stopping the build where it
// happened: TRY.
//
// A separate type because the caller has to treat it differently, and the
// difference is the whole feature. Everything downstream of the failure has
// already run, so what a `FINALLY` declared still has to be exported - and then
// the build fails. Returning a plain StepError made the CLI stop before
// exporting, so the artifact from the failed step was discarded: the build
// failed correctly and lost the one thing TRY exists to keep.
type ToleratedFailureError struct {
	*StepError
}

// Unwrap lets a caller that only cares that a step failed find the StepError.
func (e *ToleratedFailureError) Unwrap() error { return e.StepError }
