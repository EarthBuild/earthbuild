package guest

import (
	"os"
	"strings"
)

// binfmtRegister is where the kernel lists the interpreters registered for
// foreign binaries.
const binfmtRegister = "/proc/sys/fs/binfmt_misc"

// emulates names the interpreters this machine has registered and enabled.
//
// **Asked of the guest, because the guest is the machine that runs steps.**
// The host reads its own register, which under a VM backend is a different
// kernel entirely - on macOS it is not even a kernel that has one. A build
// placed a step on the strength of the host's answer and then handed it to a
// guest that could not run it, or refused a step the guest could have run
// perfectly well. Both were wrong in the same way: the question was put to the
// wrong machine.
//
// Names rather than platforms: the vocabulary that maps `x86_64` to `amd64`
// lives on the host beside the placement it informs, and two copies of it would
// disagree the day one learnt a name. This reports what the kernel says.
//
// **Never an error.** No register, an unreadable one, or an empty one all mean
// the same thing to a build - this machine emulates nothing - and only a build
// that needed emulation notices, by being refused with a message naming what to
// register.
func emulates() []string {
	// **Mounted before it is read.** `binfmt_misc` is a filesystem, and a
	// kernel that supports it still shows an empty directory until something
	// mounts it - which is what Apple's Rosetta share leaves behind: a
	// registration that is there and invisible. Best effort, because a guest
	// that may not mount it is a guest that emulates nothing, which is the
	// answer it would have given anyway.
	mountBinfmt()

	entries, err := os.ReadDir(binfmtRegister)
	if err != nil {
		return nil
	}

	var out []string

	for _, e := range entries {
		// `register` and `status` are the register's own controls, not
		// interpreters; the kernel lists them alongside.
		if e.Name() == "register" || e.Name() == "status" {
			continue
		}

		// **Registered and disabled is not available.** An entry can be turned
		// off without being removed, and a step placed on the strength of one
		// fails with an exec format error somewhere far from here.
		body, err := os.ReadFile(binfmtRegister + "/" + e.Name()) //nolint:gosec // an entry of the register being read
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(body)), "enabled") {
			continue
		}

		out = append(out, e.Name())
	}

	return out
}
