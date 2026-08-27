package exec

import (
	"os"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// binfmtRegister is where the kernel lists the interpreters registered for
// foreign binaries.
//
// A machine with qemu registered here runs another architecture's binaries
// transparently, which is what lets a step be placed on it when no machine of
// that architecture exists. Registration is the operator's - `tonistiigi/binfmt`
// and `docker run --privileged --rm tonistiigi/binfmt --install all` are the
// usual routes - and this only reads it.
const binfmtRegister = "/proc/sys/fs/binfmt_misc"

// archARM64 is spelled once, because the interpreter's name and the platform's
// architecture differ and the pair is easy to transpose.
const archARM64 = "arm64"

// qemuArch maps an interpreter's registered name to the architecture it runs.
//
// **Named rather than derived.** The kernel's entry is called `qemu-aarch64`
// and the platform is `arm64`; the two vocabularies differ for most of these,
// and guessing between them would report an architecture the engine cannot name
// and place a step nothing can run.
var qemuArch = map[string]string{
	"qemu-aarch64": archARM64,
	"qemu-arm":     "arm",
	"qemu-mips64":  "mips64",
	"qemu-ppc64le": "ppc64le",
	"qemu-riscv64": "riscv64",
	"qemu-s390x":   "s390x",
	"qemu-x86_64":  "amd64",
}

// emulatedPlatforms is what this machine can run through emulation.
//
// **Never an error.** No register, an unreadable one, or nothing recognised all
// mean the same thing to a build: this machine emulates nothing, every step is
// placed as it always was, and only a build that needed emulation notices - by
// being refused with a message naming what to register.
func emulatedPlatforms(dir string) []ir.Platform {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []ir.Platform

	for _, e := range entries {
		arch, known := qemuArch[e.Name()]
		if !known {
			continue
		}

		// **Registered and disabled is not available.** An entry can be turned
		// off without being removed, and a step placed on the strength of one
		// would fail with an exec format error somewhere far from here.
		// The path is an entry of the register this was asked to read, and the
		// name comes from the kernel's own listing of it.
		body, err := os.ReadFile(dir + "/" + e.Name()) //nolint:gosec // an entry of the register being read
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(body)), "enabled") {
			continue
		}

		out = append(out, ir.Platform{OS: "linux", Arch: arch})
	}

	// Sorted, because this reaches a Worker and a Worker reaches placement: two
	// runs of one build must consider the same machines in the same order (I12).
	sort.Slice(out, func(i, j int) bool { return out[i].Arch < out[j].Arch })

	return out
}

// EmulatedPlatforms is what this machine can run through emulation, read from
// the kernel's register.
func EmulatedPlatforms() []ir.Platform { return emulatedPlatforms(binfmtRegister) }
