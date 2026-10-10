package ignore

import (
	"fmt"
	"strings"

	"github.com/moby/patternmatcher"
)

// Patterns builds a matcher from a comma-separated list written in an Earthfile.
//
// **`CACHE --portable-except` is the caller**, where the patterns name the
// paths under a cache mount whose meaning is local to one machine, and which
// therefore may not be shared. Written inline rather than in a file, which is the only
// difference from `Read`: the syntax is one syntax, because an author who knows
// what `.earthignore` means already knows what this means.
//
// An empty list is a matcher that excludes nothing, and that is a real answer
// rather than a degenerate one - `--portable-except ”` is the strongest form
// of the claim, made about a content-addressed store with no index beside it.
// Whether the claim was made at all is carried separately, by `Mount.Portable`.
//
// Empty elements are dropped, because `'a, b,'` is what a person writes and the
// matcher reads a bare `""` as a pattern matching everything - which would
// exclude the whole cache and share nothing, silently.
func Patterns(list string) (Matcher, error) {
	var patterns []string

	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			patterns = append(patterns, p)
		}
	}

	if len(patterns) == 0 {
		return Matcher{}, nil
	}

	m, err := patternmatcher.New(patterns)
	if err != nil {
		return Matcher{}, fmt.Errorf("--portable-except %q: %w", list, err)
	}

	return Matcher{m: m}, nil
}
