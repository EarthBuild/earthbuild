package cli

import "fmt"

// progressLine is one line of a step's live output, as the reader sees it.
//
// The prefix is not decoration. Steps run concurrently, so their output
// interleaves; unattributed lines are worse than none, because a reader takes
// one step's error for another's and debugs the wrong command.
//
// `RUN --raw-output` is the case where that is precisely wrong: the step is
// writing for a parser rather than a reader, and a GitHub Actions fold marker
// is `::group::` at the start of a line and nothing anywhere else. Prefixing
// one turns a directive into a sentence about a directive (E937).
func progressLine(step, line string, raw bool) string {
	if raw {
		return line + "\n"
	}

	return fmt.Sprintf("  %-14s | %s\n", step, line)
}
