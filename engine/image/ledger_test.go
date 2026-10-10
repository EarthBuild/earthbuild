package image_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestALedgerAnswersWhenThereIsSomethingToSay.
//
// **The file marker was the whole of why streaming did not pay.** A guest
// reading a blob as it arrives has to know how far the writer has got, and
// asking the shared filesystem gave an answer about 460ms old - so the guest
// spent the fetch waiting rather than unpacking, and the head start and the
// waiting cancelled exactly (E688).
//
// In memory on the host and answered over the socket the guest already has,
// there is no filesystem in the path at all. The wait is a condition variable
// rather than a poll: the answer arrives when there is one.
func TestALedgerAnswersWhenThereIsSomethingToSay(t *testing.T) {
	t.Parallel()

	t.Run("it grew", func(t *testing.T) {
		t.Parallel()

		l := image.NewLedger()

		go func() {
			time.Sleep(10 * time.Millisecond)
			l.Set("blob", 4096)
			l.Set("blob", 8192)
		}()

		n, err := l.Await("blob", 4096, time.Minute)
		if err != nil {
			t.Fatal(err)
		}

		if n != 8192 {
			t.Errorf("waited for more than 4096 and was told %d", n)
		}
	})

	t.Run("it had already grown", func(t *testing.T) {
		t.Parallel()

		l := image.NewLedger()
		l.Set("blob", 1<<20)

		n, err := l.Await("blob", 0, time.Minute)
		if err != nil || n != 1<<20 {
			t.Errorf("an answer already available came back as (%d, %v)", n, err)
		}
	})

	t.Run("the fetch gave up", func(t *testing.T) {
		t.Parallel()

		l := image.NewLedger()

		go func() {
			time.Sleep(10 * time.Millisecond)
			l.Fail("blob", errors.New("the registry hung up"))
		}()

		_, err := l.Await("blob", 0, time.Minute)
		if err == nil || !strings.Contains(err.Error(), "hung up") {
			t.Errorf("a reader waiting on a failed fetch got %v, want the reason", err)
		}
	})

	t.Run("nothing happened", func(t *testing.T) {
		t.Parallel()

		l := image.NewLedger()

		_, err := l.Await("blob", 0, 40*time.Millisecond)
		if err == nil {
			t.Fatal("waiting on a blob nobody is fetching succeeded")
		}

		if !strings.Contains(err.Error(), "blob") {
			t.Errorf("the timeout does not name what was waited for:\n  %v", err)
		}
	})

	// **A failure must wake every waiter, not one.** Five layers stream at once
	// and a fetch that dies while several are blocked would otherwise leave the
	// rest waiting for their own timeout - minutes of a build spent on an
	// answer that already exists.
	t.Run("everyone waiting hears about a failure", func(t *testing.T) {
		t.Parallel()

		l := image.NewLedger()
		out := make(chan error, 3)

		for range 3 {
			go func() {
				_, err := l.Await("blob", 0, time.Minute)
				out <- err
			}()
		}

		time.Sleep(10 * time.Millisecond)
		l.Fail("blob", errors.New("gone"))

		for range 3 {
			select {
			case err := <-out:
				if err == nil {
					t.Error("a waiter was told the fetch succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("a waiter was never woken, so a dead fetch costs every" +
					" reader its full patience")
			}
		}
	})
}
