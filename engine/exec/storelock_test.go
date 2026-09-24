package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One sandbox at a time owns a store device.
//
// **Because two would corrupt it.** A block device is not a directory: two
// guests mounting one XFS filesystem read-write is not a race that sometimes
// loses an update, it is two kernels with two independent logs writing the same
// metadata. The store survives neither.
func TestOnlyOneSandboxHoldsAStoreDevice(t *testing.T) {
	t.Parallel()

	at := deviceFile(t)

	release, err := claimStore(at)
	if err != nil {
		t.Fatal(err)
	}

	defer release()

	_, err = claimStore(at)
	if err == nil {
		t.Fatal("two sandboxes hold one store device, and it will not survive both")
	}

	// The message has to say what to do: the second build is not broken, it is
	// second.
	if !strings.Contains(err.Error(), at) {
		t.Errorf("the refusal does not name the device: %v", err)
	}
}

// Released, it is available again - a build that ends does not leave the device
// unusable until the machine reboots.
func TestAReleasedDeviceCanBeClaimedAgain(t *testing.T) {
	t.Parallel()

	at := deviceFile(t)

	release, err := claimStore(at)
	if err != nil {
		t.Fatal(err)
	}

	release()

	release, err = claimStore(at)
	if err != nil {
		t.Fatalf("the device stayed held after its holder released it: %v", err)
	}

	release()
}

// Two different devices are two different claims, so two builds with their own
// stores run at once.
func TestTwoDevicesAreTwoClaims(t *testing.T) {
	t.Parallel()

	first, err := claimStore(deviceFile(t))
	if err != nil {
		t.Fatal(err)
	}

	defer first()

	second, err := claimStore(deviceFile(t))
	if err != nil {
		t.Fatalf("a second device could not be claimed: %v", err)
	}

	second()
}

// No device is nothing to claim, and not a failure: a sandbox may be started
// without one.
func TestNoDeviceIsNothingToClaim(t *testing.T) {
	t.Parallel()

	release, err := claimStore("")
	if err != nil {
		t.Fatal(err)
	}

	release()
}

func deviceFile(t *testing.T) string {
	t.Helper()

	at := filepath.Join(t.TempDir(), "store.img")

	if err := os.WriteFile(at, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	return at
}
