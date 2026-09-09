//go:build linux

package exec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// vmRecord is where a running guest is and what it was built as.
//
// **Because a microVM has no `container ls`.** The Apple backend finds the VM
// the last build left running by asking its runtime what is up. Firecracker has
// no runtime to ask - a VMM is a process with a socket and nothing enumerates
// it - so this register is what makes the machine findable at all.
type vmRecord struct {
	// Digest names what the machine is, so a build never attaches to one
	// configured for something else. See sandboxDigest.
	Digest string `json:"digest"`
	// Vsock is the socket the agent answers on.
	Vsock string `json:"vsock"`
	// PID is the VMM, which is the cheap half of liveness.
	PID int `json:"pid"`
}

// vmRecordPath is the register for a store, beside the store.
//
// **Keyed on the store rather than on the configuration**, because the thing
// that must not happen twice is two guests mounting one filesystem. A register
// per configuration would let a build with different settings boot a second
// machine on the same device, which is the fault claimStore exists to prevent -
// so every configuration for a device shares one register, and a build that
// wants a machine other than the one recorded replaces it rather than joining
// it.
func vmRecordPath(store string) string {
	return store + ".vm"
}

// writeVMRecord records a running machine.
//
// Written to a neighbour and renamed, so a reader never sees half of one: a
// truncated record is a build that cannot find a machine that is there, which
// costs a boot and, worse, invites a second machine onto the device.
func writeVMRecord(store string, rec vmRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err //nolint:wrapcheck // a struct of three fields cannot fail to marshal
	}

	at := vmRecordPath(store)

	tmp, err := os.CreateTemp(filepath.Dir(at), ".vm-*")
	if err != nil {
		return err //nolint:wrapcheck // the caller says what it was doing
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(b)
	if err != nil {
		_ = tmp.Close()

		return err //nolint:wrapcheck // as above
	}

	err = tmp.Close()
	if err != nil {
		return err //nolint:wrapcheck // as above
	}

	return os.Rename(tmp.Name(), at) //nolint:wrapcheck // as above
}

// readVMRecord reads the register, reporting whether it named a machine.
//
// Absent, unreadable and unparseable are all "no machine": this is a note of
// where something is, and the worst a bad one may cost is a boot that was not
// needed. Stopping the build over it would be worse than the fault.
func readVMRecord(store string) (vmRecord, bool) {
	b, err := os.ReadFile(vmRecordPath(store))
	if err != nil {
		return vmRecord{}, false
	}

	var rec vmRecord

	err = json.Unmarshal(b, &rec)
	if err != nil || rec.PID <= 0 || rec.Vsock == "" {
		return vmRecord{}, false
	}

	return rec, true
}

// forgetVMRecord removes the register, for a machine that has gone.
func forgetVMRecord(store string) {
	_ = os.Remove(vmRecordPath(store))
}

// vmAlive reports whether the recorded VMM is still running.
//
// Signal 0, which checks for a process without disturbing it. The cheap half of
// liveness: a pid can be reused and a running VMM can still be wedged, so the
// honest half is the handshake the caller does next.
func vmAlive(rec vmRecord) bool {
	if rec.PID <= 0 {
		return false
	}

	return unix.Kill(rec.PID, 0) == nil
}

// vmIsOurs reports whether a live pid is a firecracker this engine started.
//
// **Because pids are reused.** A record naming a pid that has since become
// somebody's editor would otherwise read as a running guest, and the build
// would dial a socket nobody is listening on and wait out the boot timeout.
// Cheap, and wrong only in the safe direction: a VMM this cannot confirm is
// treated as gone, which costs a boot.
func vmIsOurs(rec vmRecord) bool {
	b, err := os.ReadFile(filepath.Join("/proc", itoa(rec.PID), "cmdline"))
	if err != nil {
		return false
	}

	return strings.Contains(string(b), "firecracker")
}

// itoa keeps the /proc path free of a strconv import in a file about records.
func itoa(n int) string { return strconv.Itoa(n) }
