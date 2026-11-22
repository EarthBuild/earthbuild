package fleet

import "context"

// warming is a source that can open its connection before anything needs it.
type warming interface {
	Warm(ctx context.Context)
}

// warmAll opens connections to these sources, without waiting for any of them.
//
// **Reaching a peer costs more than reading from it.** Measured between two
// GitHub runners: 7.9 MiB read in 302ms, against 403ms to 3363ms spent getting
// to the machine holding it - discovery, a handshake, hole punching, none of it
// proportional to what is being fetched. On a small build that *is* the fleet's
// cost, and it lands on the critical path because a connection is opened by the
// first fetch that wants one (E-F1).
//
// Called where the holders first become known, which on a prime is before any
// step needs them. Sources that have no connection - `unreachable`, an
// in-process store - are skipped rather than special-cased.
func warmAll(ctx context.Context, srcs []Source) {
	for _, s := range srcs {
		if w, ok := s.(warming); ok {
			w.Warm(ctx)
		}
	}
}
