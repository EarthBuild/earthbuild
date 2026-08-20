package cli

import (
	"fmt"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/cache"
)

// shortDigest is how much of a digest is worth printing. Twelve hex characters
// is enough to find an entry on disk and short enough that three of them fit on
// a line beside their labels.
const shortDigest = 12

// conflictWarning describes cache keys that claimed two different results.
//
// The cache refuses such a rewrite, which is what keeps state insert-or-remove
// (green paper I9). Refusing on its own would be the worse half: a key
// determines a result by construction - Κ₂ hashes the operation, the
// environment and the platform along with everything the step observed (4.6) -
// so two layers under one key means a step read the same things and produced
// different output. Kept quiet, that becomes a step which misses the cache on
// every build forever, with nothing anywhere saying why.
//
// Returns the empty string when there is nothing to say. A diagnostic that
// appears on healthy builds is trained away inside a week, and is then absent
// from the build that needed it.
func conflictWarning(recorded []cache.Conflict, total int) string {
	if total == 0 {
		return ""
	}

	var b strings.Builder

	fmt.Fprintf(&b, "  warning: %s claimed two different results\n", plural(total, "cache key"))
	b.WriteString("    a step read the same inputs twice and produced different output," +
		" so its result is not reproducible\n")

	for _, c := range recorded {
		fmt.Fprintf(&b, "    %s  held %s, then produced %s\n",
			short(c.Key.String()), short(c.Held.String()), short(c.Given.String()))
	}

	// The recorded list is capped. Presenting a capped list as the whole list is
	// a build under-reporting how wrong it is.
	if rest := total - len(recorded); rest > 0 {
		fmt.Fprintf(&b, "    (%d more not listed)\n", rest)
	}

	return b.String()
}

func short(s string) string {
	if len(s) <= shortDigest {
		return s
	}

	return s[:shortDigest]
}

func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}

	return fmt.Sprintf("%d %ss", n, thing)
}
