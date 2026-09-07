//go:build darwin

package exec

import (
	"debug/elf"
	"fmt"
)

// The guest-binary architecture check belongs to the Apple sandbox, which is
// the only backend that hands a Linux binary to a machine it did not compile it
// for. Tagged to that platform rather than left where `unused` is right about
// it: elf.go itself stays untagged, because elfArch is read on every platform.
// checkGuestArch verifies the agent will run in the sandbox before it is exec'd.
//
// The kernel's answer to a wrong-architecture binary is ENOEXEC - "Exec format
// error" - which through a VM's control plane arrives as an internal error from
// a component the user has never heard of, naming neither the file nor the
// architecture. Reading four bytes of ELF header here turns that into a sentence.
//
// A non-ELF or unreadable file is not an error: it may be a wrapper script, and
// refusing something that would have worked is worse than a poor message.
func checkGuestArch(path, wantArch string) error {
	f, err := elf.Open(path)
	if err != nil {
		// Not an ELF at all, which on this platform means somebody built the
		// guest for darwin. Said here rather than left to exec: the sandbox
		// reports it from inside the VM as `Exec format error`, twice, followed
		// by a handshake timeout - which names neither the file nor the fix,
		// and this check knew both (E490).
		return fmt.Errorf(
			"%s is not a Linux executable, and the sandbox runs Linux"+
				"\n  %v"+
				"\n  rebuild it: CGO_ENABLED=0 GOOS=linux GOARCH=%s go build"+
				" -o %s ./cmd/earth-guestd",
			path, err, wantArch, path)
	}

	defer f.Close()

	want, known := elfArch[wantArch]
	if !known {
		return nil
	}

	if f.Machine == want {
		return nil
	}

	return fmt.Errorf(
		"%s is built for %s, but the sandbox runs %s"+
			"\n  rebuild it: GOOS=linux GOARCH=%s go build -o %s ./cmd/earth-guestd",
		path, machineName(f.Machine), wantArch, wantArch, path)
}

func machineName(m elf.Machine) string {
	for arch, machine := range elfArch {
		if machine == m {
			return arch
		}
	}

	return m.String()
}
