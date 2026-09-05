package exec

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
)

// probePaths are where an unpacked image keeps something worth reading.
//
// Ordered by how likely they are to exist and to be a real binary rather than a
// script. busybox first because an alpine-based image is one file, and every
// other name in that image is a link to it.
var probePaths = []string{
	"bin/busybox",
	"bin/sh",
	"bin/dash",
	"usr/bin/env",
	"bin/cat",
	"bin/ls",
}

// archOfTree reports the architecture an unpacked image is built for, by
// reading it rather than by believing anything.
//
// The image cache is keyed on reference *and* platform, so an entry under the
// arm64 key claims to be an arm64 image. On this machine one is not: the entry
// for `hashicorp/terraform:light` under `linux/arm64` holds an x86-64 busybox.
// The stored configuration cannot contradict the key, because it records what
// the image declares about Env, Entrypoint and Labels and not its architecture.
// The files can.
//
// Reported as unknown rather than as an error when nothing can be read: a
// scratch image, a distroless one, or anything whose entrypoint is a script has
// nothing here to read, and refusing those would refuse images that work. That
// is the rule checkArchitecture already follows for an image that declares
// nothing about itself.
func archOfTree(root string) (string, bool) {
	for _, p := range probePaths {
		// Inside the tree on purpose: `bin/sh` in an unpacked image is usually
		// a symlink to an absolute path like /bin/busybox, which resolves
		// against *this* machine's root - where it either does not exist or,
		// far worse, does.
		at := filepath.Join(root, p)

		fi, err := os.Lstat(at)
		if err != nil {
			continue
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(at)
			if linkErr != nil {
				continue
			}

			if filepath.IsAbs(target) {
				at = filepath.Join(root, target)
			} else {
				at = filepath.Join(filepath.Dir(at), target)
			}
		}

		f, err := elf.Open(at)
		if err != nil {
			continue
		}

		machine := f.Machine

		_ = f.Close()

		for arch, m := range elfArch {
			if m == machine {
				return arch, true
			}
		}
	}

	return "", false
}

// agreesWithKey reports whether an unpacked entry is the architecture its cache
// key claims.
//
// True when the tree cannot be read, which is the same rule the architecture
// check follows for an image that declares nothing about itself: a scratch or
// distroless image has nothing here to read, and discarding those on every
// build would turn a defence into a cache that never hits.
func agreesWithKey(dir, platform string) bool {
	got, known := archOfTree(dir)
	if !known || platform == "" {
		return true
	}

	_, arch, ok := strings.Cut(platform, "/")
	if !ok {
		return true
	}

	// A variant - linux/arm/v7 - narrows the architecture rather than changing
	// it, and an ELF header does not carry it.
	arch, _, _ = strings.Cut(arch, "/")

	return got == arch
}
