package exec

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildProbeELF writes a minimal ELF64 executable, with or without an
// interpreter.
//
// Synthesised rather than compiled. Compiling a dynamic binary needs a C
// toolchain, compiling a static one needs the right flags, and neither is
// available on every machine that runs these tests - and on macOS `go build`
// produces Mach-O, so the test would be about the host rather than about the
// discriminator. Sixty-four bytes of header and one program header entry is
// exactly the thing under test and nothing else.
func buildProbeELF(t *testing.T, static bool) string {
	t.Helper()

	const (
		ehsize    = 64
		phentsize = 56
	)

	interp := "/lib64/ld-linux-x86-64.so.2\x00"

	var b bytes.Buffer

	b.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0})

	put := func(v any) {
		err := binary.Write(&b, binary.LittleEndian, v)
		if err != nil {
			t.Fatal(err)
		}
	}

	phnum := uint16(0)
	if !static {
		phnum = 1
	}

	put(uint16(2))         // e_type: ET_EXEC
	put(uint16(0x3e))      // e_machine: x86-64
	put(uint32(1))         // e_version
	put(uint64(0))         // e_entry
	put(uint64(ehsize))    // e_phoff
	put(uint64(0))         // e_shoff
	put(uint32(0))         // e_flags
	put(uint16(ehsize))    // e_ehsize
	put(uint16(phentsize)) // e_phentsize
	put(phnum)             // e_phnum
	put(uint16(64))        // e_shentsize
	put(uint16(0))         // e_shnum
	put(uint16(0))         // e_shstrndx

	if !static {
		off := uint64(ehsize + phentsize)

		put(uint32(3))           // p_type: PT_INTERP
		put(uint32(4))           // p_flags: R
		put(off)                 // p_offset
		put(uint64(0))           // p_vaddr
		put(uint64(0))           // p_paddr
		put(uint64(len(interp))) // p_filesz
		put(uint64(len(interp))) // p_memsz
		put(uint64(1))           // p_align

		b.WriteString(interp)
	}

	name := "static"
	if !static {
		name = "dynamic"
	}

	p := filepath.Join(t.TempDir(), name)

	err := os.WriteFile(p, b.Bytes(), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// The fixture is an ELF the standard library agrees with.
//
// A synthesised binary that `debug/elf` cannot parse would make both linkage
// tests pass for the wrong reason - "cannot parse" and "no interpreter" must
// not be the same answer, which is the E97 shape.
func TestTheELFFixturesAreParseable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		static bool
		want   bool
	}{{true, false}, {false, true}} {
		dynamic, err := needsAnInterpreter(buildProbeELF(t, tc.static))
		if err != nil {
			t.Errorf("static=%v: the fixture did not parse as ELF: %v", tc.static, err)

			continue
		}

		if dynamic != tc.want {
			t.Errorf("static=%v: reported needing an interpreter = %v", tc.static, dynamic)
		}
	}
}
