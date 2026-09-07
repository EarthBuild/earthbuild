package exec

import "debug/elf"

// elfArch maps a Go architecture name to the ELF machine it produces.
var elfArch = map[string]elf.Machine{
	"amd64":   elf.EM_X86_64,
	"arm64":   elf.EM_AARCH64,
	"arm":     elf.EM_ARM,
	"386":     elf.EM_386,
	"riscv64": elf.EM_RISCV,
	"ppc64le": elf.EM_PPC64,
	"s390x":   elf.EM_S390,
}
