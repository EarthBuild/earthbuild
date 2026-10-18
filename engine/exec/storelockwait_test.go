package exec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A claim waits briefly for a guest that is still going away.
//
// **Because the refusal was answering a question nobody asked.** A serial
// corpus run - one build at a time, by construction - was refused 36 times in
// 246 with `the store device is in use by another build`, and the holder had
// already gone by the time the message was written: /proc/locks named nobody,
// microseconds after flock had said the lock was held. There was no second
// build. There was a previous sandbox whose VMM had not finished putting the
// device down.
//
// Waiting for a *running* build would be the hang the non-blocking claim was
// written to avoid, which is why this is bounded and short: long enough for a
// teardown, far too short to sit behind somebody's build.
func TestAClaimWaitsForAGuestStillShuttingDown(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "store.img")

	err := os.WriteFile(at, make([]byte, 4096), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	held, err := claimStore(at)
	if err != nil {
		t.Fatalf("the first claim was refused: %v", err)
	}

	// Let go while the second claim is waiting, the way a VMM does.
	go func() {
		time.Sleep(150 * time.Millisecond)
		held()
	}()

	start := time.Now()

	release, err := claimStore(at)
	if err != nil {
		t.Fatalf("a claim on a device being released was refused after %v: %v",
			time.Since(start), err)
	}

	release()

	if time.Since(start) < 100*time.Millisecond {
		t.Errorf("the claim returned in %v, so it cannot have waited for the"+
			" release - the test is not testing what it says", time.Since(start))
	}
}

// A device genuinely in use is still refused, and within the bound.
func TestAClaimOnABusyDeviceIsStillRefused(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "store.img")

	err := os.WriteFile(at, make([]byte, 4096), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	held, err := claimStore(at)
	if err != nil {
		t.Fatal(err)
	}

	defer held()

	start := time.Now()

	_, err = claimStore(at)
	if err == nil {
		t.Fatal("a second claim on a held device succeeded")
	}

	if took := time.Since(start); took > 2*storeClaimPatience {
		t.Errorf("the refusal took %v against a %v bound; a build refused"+
			" should be told quickly", took, storeClaimPatience)
	}
}
