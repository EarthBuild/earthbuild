package guest

import (
	"strings"
	"sync"
)

// tailKeeps is how much of a daemon's output is worth carrying into an error.
//
// The end, not the beginning: a daemon that fails prints its startup chatter
// first and the reason last, so a head-limited buffer keeps exactly the part
// nobody needs.
const tailKeeps = 2048

// tail keeps the last of what was written to it, and nothing else.
//
// Bounded because a daemon that runs for an hour writes more than an error
// should carry, and because holding all of it to show four lines is a leak with
// a long fuse.
type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b = append(t.b, p...)
	if len(t.b) > tailKeeps {
		t.b = t.b[len(t.b)-tailKeeps:]
	}

	return len(p), nil
}

// String is the kept tail, trimmed, and empty when nothing was written.
func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return strings.TrimSpace(string(t.b))
}
