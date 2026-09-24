package layer

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// IDMap translates ids as a user namespace does.
//
// The kernel's own form, from `/proc/pid/uid_map`: triples of "this id inside,
// this id outside, this many". Parsed rather than assumed, because this engine
// writes two of them - `0 <euid> 1` for the invoking user and
// `1 <subuid> 65536` for the delegated range (E105) - and a translation that
// knew only the first would be right for root and wrong for every user a step
// drops to.
//
// The zero value is the identity, which is what a process with no mapping needs:
// it sees exactly what the store holds, and translating would be the error
// rather than the fix.
type IDMap struct{ ranges []idRange }

type idRange struct{ inside, outside, count uint32 }

// Outside is what an id inside the namespace is called outside it.
//
// An id in no range is returned unchanged. Inventing an answer for an unmapped
// id would be worse than declining to: an unmapped id is one the namespace
// cannot name, so nothing it owns can be observed anyway.
func (m IDMap) Outside(id uint32) uint32 {
	for _, r := range m.ranges {
		if id >= r.inside && id-r.inside < r.count {
			return r.outside + (id - r.inside)
		}
	}

	return id
}

// Empty reports whether this map translates nothing.
func (m IDMap) Empty() bool { return len(m.ranges) == 0 }

// ParseIDMap reads the kernel's mapping format.
//
// A malformed line is refused rather than skipped. Half a mapping translates
// some ids and not others, so the disagreement it causes is intermittent and
// looks like a cache that sometimes works - which is the hardest kind of wrong
// to attribute.
func ParseIDMap(r io.Reader) (IDMap, error) {
	var m IDMap

	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}

		f := strings.Fields(line)
		if len(f) != 3 {
			return IDMap{}, fmt.Errorf("id map line %q has %d fields, want 3", line, len(f))
		}

		var got [3]uint32

		for i, v := range f {
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return IDMap{}, fmt.Errorf("id map line %q: %w", line, err)
			}

			got[i] = uint32(n)
		}

		m.ranges = append(m.ranges, idRange{inside: got[0], outside: got[1], count: got[2]})
	}

	err := s.Err()
	if err != nil {
		return IDMap{}, fmt.Errorf("read the id map: %w", err)
	}

	return m, nil
}

// MapOf builds a map from literal triples, for a caller that knows its own
// mapping without a file to read - a test, or a host that just wrote one.
func MapOf(triples ...[3]uint32) IDMap {
	var m IDMap

	for _, t := range triples {
		m.ranges = append(m.ranges, idRange{inside: t[0], outside: t[1], count: t[2]})
	}

	return m
}

// OneID is a map of a single id, as a shared store's ownership shift is.
//
// A sandbox that presents an entire store as owned by root has done exactly one
// translation, and expressing it as a `uid_map` line is the same statement in the
// form the digest already understands: "what you see as `inside` is `outside` in
// the store" (E494).
// The arguments are in the order a `uid_map` line writes them - inside first -
// because the whole point of the type is that a mapping has a direction, and a
// helper that took them the other way round would be the easiest thing in this
// file to get backwards. It was, once, on the way in.
func OneID(inside, outside uint32) IDMap {
	return IDMap{ranges: []idRange{{inside: inside, outside: outside, count: 1}}}
}
