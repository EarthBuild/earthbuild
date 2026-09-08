package cli_test

import "regexp"

// volatile are the parts of a failure that differ between two reports of the
// same fault: the layer it happened on, and the directory it happened in.
//
// **The gate ranks its work list by group size**, so a cause that carries a
// hash or a temporary path in its message arrives as one group per occurrence.
// A corrupt store device produced 146 of 179 failures and 146 groups of one;
// the list said the biggest problem was a missing Dockerfile, five times over.
var volatile = []struct {
	what *regexp.Regexp
	with string
}{
	// A layer id, which names the step rather than the fault.
	{regexp.MustCompile(`\b[0-9a-f]{32,}\b`), "<layer>"},
	// A worker's copy of the tree, which is per-run and per-worker.
	{regexp.MustCompile(`/tmp/[^\s:]+`), "<path>"},
	// A line number is part of the cause; the file it is in may not be.
	{regexp.MustCompile(`\b\d{4,}\b`), "<n>"},
}

// groupOf is the key two failures share when they have one cause.
func groupOf(reason string) string {
	for _, v := range volatile {
		reason = v.what.ReplaceAllString(reason, v.with)
	}

	return reason
}
