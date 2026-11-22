package layer

import "sort"

// matcher finds any of several byte strings in one pass.
//
// **The scan was one `bytes.Contains` per secret**, so ten credentials meant ten
// passes over every byte of a layer - the cost growing with the number of things
// worth protecting, which is the wrong way round.
//
// Aho-Corasick reads each byte once whatever the count: a trie of the values,
// with a failure link from every node to the longest proper suffix that is also
// a prefix of some value, so a mismatch resumes rather than restarting.
//
// It also removes the reason a chunked read needed an overlap. The state is the
// node the walk is standing on, and it survives between chunks - so a credential
// split across two reads is matched without anybody keeping a tail.
type matcher struct {
	// next is the goto function, sparse because a credential's alphabet is tiny
	// beside 256.
	next []map[byte]int
	// fail is where to resume when the byte does not continue this node.
	fail []int
	// ends names every secret whose value finishes at this node. A list, not a
	// single name: one value may be a suffix of another, and both have leaked.
	ends [][]string
	// at is the node the walk is standing on, carried between writes.
	at int
	// seen is what has been found, by name, deduplicated - a caller wants to
	// know which credential leaked, not how often.
	seen map[string]bool
}

// newMatcher builds the automaton, or nil when there is nothing to look for.
func newMatcher(secrets []Secret) *matcher {
	m := &matcher{
		next: []map[byte]int{{}},
		fail: []int{0},
		ends: [][]string{nil},
		seen: map[string]bool{},
	}

	live := 0

	for _, s := range secrets {
		if s.Value == "" {
			// An empty value matches everywhere; a secret nobody supplied would
			// otherwise report the whole layer.
			continue
		}

		live++
		at := 0

		for i := range len(s.Value) {
			c := s.Value[i]

			to, ok := m.next[at][c]
			if !ok {
				to = len(m.next)
				m.next = append(m.next, map[byte]int{})
				m.fail = append(m.fail, 0)
				m.ends = append(m.ends, nil)
				m.next[at][c] = to
			}

			at = to
		}

		m.ends[at] = append(m.ends[at], s.Name)
	}

	if live == 0 {
		return nil
	}

	m.link()

	return m
}

// link fills in the failure edges, breadth first, so that a node's failure is
// always computed after the shallower node it points at.
func (m *matcher) link() {
	queue := make([]int, 0, len(m.next))

	for c, to := range m.next[0] {
		_ = c
		m.fail[to] = 0

		queue = append(queue, to)
	}

	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]

		for c, to := range m.next[at] {
			back := m.fail[at]

			for back != 0 {
				if _, ok := m.next[back][c]; ok {
					break
				}

				back = m.fail[back]
			}

			if nxt, ok := m.next[back][c]; ok && nxt != to {
				m.fail[to] = nxt
			} else {
				m.fail[to] = 0
			}

			// **What the failure node ends, this node ends too.** A value that
			// is a suffix of another finishes wherever the longer one does, and
			// both have leaked.
			m.ends[to] = append(m.ends[to], m.ends[m.fail[to]]...)

			queue = append(queue, to)
		}
	}
}

// write feeds the next piece of the text through the automaton.
func (m *matcher) write(b []byte) {
	if m == nil {
		return
	}

	for _, c := range b {
		for {
			if to, ok := m.next[m.at][c]; ok {
				m.at = to

				break
			}

			if m.at == 0 {
				break
			}

			m.at = m.fail[m.at]
		}

		for _, name := range m.ends[m.at] {
			m.seen[name] = true
		}
	}
}

// found is every secret seen so far, sorted so two runs report the same thing in
// the same order (I12).
func (m *matcher) found() []string {
	if m == nil || len(m.seen) == 0 {
		return nil
	}

	out := make([]string, 0, len(m.seen))
	for name := range m.seen {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}

// reset forgets the text but keeps the automaton, so one build's secrets are
// compiled once and every file reuses them.
func (m *matcher) reset() {
	if m == nil {
		return
	}

	m.at = 0
	m.seen = map[string]bool{}
}
