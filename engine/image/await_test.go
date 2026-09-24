package image_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestWaitingForABlobToGrow.
//
// Three outcomes and no fourth: it grew, the fetch gave up, or nothing happened
// for long enough that waiting further is not a plan. **A reader with no
// deadline is a build that hangs with nothing to say**, which this engine has
// already produced once and taken some trouble to diagnose (E673).
func TestWaitingForABlobToGrow(t *testing.T) {
	t.Parallel()

	t.Run("it grew", func(t *testing.T) {
		t.Parallel()

		blob := filepath.Join(t.TempDir(), "sha256-a")

		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = image.WriteProgress(blob, 8192)
		}()

		n, err := image.AwaitProgress(blob, 4096, time.Minute)
		if err != nil {
			t.Fatal(err)
		}

		if n != 8192 {
			t.Errorf("waited for more than 4096 and got %d", n)
		}
	})

	t.Run("the fetch gave up", func(t *testing.T) {
		t.Parallel()

		blob := filepath.Join(t.TempDir(), "sha256-b")

		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = image.WriteProgressFailure(blob, errors.New("the registry hung up"))
		}()

		_, err := image.AwaitProgress(blob, 0, time.Minute)
		if err == nil || !strings.Contains(err.Error(), "hung up") {
			t.Errorf("a reader waiting on a failed fetch got %v, want the reason", err)
		}
	})

	t.Run("nothing happened", func(t *testing.T) {
		t.Parallel()

		blob := filepath.Join(t.TempDir(), "sha256-c")

		_, err := image.AwaitProgress(blob, 0, 50*time.Millisecond)
		if err == nil {
			t.Fatal("waiting for a blob nobody is writing succeeded")
		}

		// The message has to name the blob and say what was being waited for,
		// or it is indistinguishable from every other timeout in the engine.
		for _, want := range []string{"sha256-c", "0"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the timeout does not mention %q:\n  %v", want, err)
			}
		}
	})
}
