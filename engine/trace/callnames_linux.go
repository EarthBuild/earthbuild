//go:build linux

package trace

import "strconv"

// slotOf is each traced syscall's index in `traced`.
//
// Built once, so counting a notification is an index rather than a search. The
// notification loop is the thing being measured, and an instrument that costs
// what it measures reports its own overhead.
var slotOf = func() map[int32]int {
	m := make(map[int32]int, len(traced))

	for i, nr := range traced {
		m[int32(nr)] = i //nolint:gosec // a syscall number fits
	}

	return m
}()

// callName writes a traced syscall the way a manual page does.
//
// A table rather than a lookup: `golang.org/x/sys/unix` has no reverse mapping,
// and a bare number in a report is a number somebody then has to go and look
// up - the argument `pollEvents` makes one file over about "0x18".
//
// Per architecture, because the numbers are. A name this does not know is its
// number, which is still better than nothing and cannot be wrong.
func callName(nr uint32) string {
	if s, ok := callNames[nr]; ok {
		return s
	}

	return strconv.FormatUint(uint64(nr), 10)
}
