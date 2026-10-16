package exec

import (
	"sort"
	"strings"
)

// stepEnv is the environment a step runs with: what its base declared, with
// what the Earthfile said laid over the top.
//
// **The base's declaration was being dropped entirely.** Only `--entrypoint`
// read it, so a step stood on `golang` without `/usr/local/go/bin` on its PATH
// and on `distroless/python3` without `/usr/bin`. The shell form hid it - `sh`
// supplies a default PATH of its own - which is why every corpus entry that
// noticed was an exec-form one.
//
// The declaration is an input the step's key already covers, since it arrives
// with the base, so inheriting it is not the ambient state I3 forbids.
//
// Order is fixed rather than incidental: the declaration's own order, then the
// Earthfile's additions sorted. `n.Op.Env` is a map, and ranging over it gave a
// step a different environment on every run of the same build.
func stepEnv(declared []string, planned map[string]string) []string {
	out := make([]string, 0, len(declared)+len(planned))
	laid := make(map[string]bool, len(planned))

	for _, e := range declared {
		name, _, ok := strings.Cut(e, "=")
		if v, over := planned[name]; ok && over {
			laid[name] = true
			e = name + "=" + v
		}

		out = append(out, e)
	}

	rest := make([]string, 0, len(planned))

	for k := range planned {
		if !laid[k] {
			rest = append(rest, k)
		}
	}

	sort.Strings(rest)

	for _, k := range rest {
		out = append(out, k+"="+planned[k])
	}

	return out
}
