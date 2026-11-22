package image

import "testing"

// TestAPartlyWrittenPageIsNeverRead.
//
// **The page is the unit, not the byte.** A read that touches a page pulls the
// whole page into the cache, and if the writer has only filled part of it the
// rest is zeros - which are then cached, and returned again when those bytes
// really do arrive. That is E683's failure at a finer grain, and it is worse,
// because it corrupts the middle of a layer rather than stopping the read:
// `archive/tar: invalid tar header`, intermittently, depending on where the
// writer's announcements happen to fall.
//
// So a reader takes only whole pages, and waits for the rest.
//
// The exception is the end. A blob's last page is short by definition, and the
// only announcement that reaches the final byte is the one the digest releases -
// so when the writer says the whole thing is there, the whole thing is there.
func TestAPartlyWrittenPageIsNeverRead(t *testing.T) {
	t.Parallel()

	const size = 10_000

	for _, c := range []struct {
		name        string
		valid, want int64
	}{
		{"nothing yet", 0, 0},
		{"part of the first page", 100, 0},
		{"exactly one page", 4096, 4096},
		{"a page and a bit", 5000, 4096},
		{"two pages and a bit", 9000, 8192},
		{"everything, which the digest released", size, size},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := usableEnd(c.valid, size); got != c.want {
				t.Errorf("with %d of %d bytes written, a reader may take %d, want %d",
					c.valid, size, got, c.want)
			}
		})
	}

	// A blob smaller than a page is all or nothing: there is no whole page to
	// take, so the reader waits for the digest rather than reading a page the
	// writer is still in the middle of.
	if got := usableEnd(300, 500); got != 0 {
		t.Errorf("a short blob offered %d bytes of a partly written page", got)
	}

	if got := usableEnd(500, 500); got != 500 {
		t.Errorf("a complete short blob was withheld at %d", got)
	}
}
