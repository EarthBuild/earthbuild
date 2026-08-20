package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// probeGroup is a group the process belongs to that is not its primary one, and
// false when there is none.
//
// A *group* rather than a uid, and the difference is the whole accuracy of this
// check. The first version handed the probe file to uid 1, which only root may
// do - so on an unprivileged Linux machine it reported "the store does not
// allow ownership to be set" about an ext4 filesystem that carries ownership
// perfectly well. It conflated *this process may not chown* with *this
// filesystem discards ownership*, which are the two things the probe exists to
// tell apart.
//
// Any process may hand a file to a group it belongs to, so a group that does
// not stick is a filesystem that discarded it. Measured on the machine that
// forced this: a macOS share swallows it, and Linux keeps it whether the caller
// is root or not.
// probed is what the probe asked for, and whether it could ask at all.
type probed struct {
	id int
	ok bool
}

// probeOwner hands the file to another owner, preferring a uid.
//
// Reports which kind succeeded, because the readback has to compare the same
// field it set.
func probeOwner(chown func(string, int, int) error, path string) (probed, bool) {
	// 1 is `daemon` on every Linux image this engine runs and is not the uid
	// anything here runs as, so a readback still showing the caller means the
	// store discarded the change.
	const otherUID = 1

	if os.Getuid() != otherUID {
		err := chown(path, otherUID, os.Getgid())
		if err == nil {
			return probed{id: otherUID, ok: true}, true
		}
	}

	gid, ok := probeGroup()
	if !ok {
		return probed{}, false
	}

	err := chown(path, os.Getuid(), gid)
	if err != nil {
		return probed{}, false
	}

	return probed{id: gid, ok: true}, false
}

func probeGroup() (int, bool) {
	groups, err := os.Getgroups()
	if err != nil {
		return 0, false
	}

	for _, g := range groups {
		if g != os.Getgid() {
			return g, true
		}
	}

	return 0, false
}

// checkStoreOwnership reports whether the layer store can carry uid and gid.
//
// It writes a file, hands it to another user, and **reads it back**. The
// readback is the whole check: a share that silently maps ownership - virtiofs
// onto macOS, which is how this engine's store reaches a VM (E1b) - accepts the
// chown, returns no error, and keeps its own answer. Only the stat afterwards
// can tell.
//
// chown is a parameter so the failure can be tested without a filesystem that
// has the fault, which is most of them.
func checkStoreOwnership(dir string, chown func(path string, uid, gid int) error) error {
	probe := filepath.Join(dir, ".ownership-probe")

	err := os.WriteFile(probe, nil, 0o600)
	if err != nil {
		return fmt.Errorf("--keep-own: cannot check whether %s carries ownership: %w", dir, err)
	}

	defer func() { _ = os.Remove(probe) }()

	// A **uid** first, and a group only where a uid is impossible.
	//
	// This is where the check earns its keep, and where a simpler version of it
	// got the answer wrong in the direction that ships bad images. Handing the
	// file to another uid is what only root may do - and the guest *is* root,
	// inside the VM, which is the case that decides whether a build delivers
	// the ownership it was asked for. A group probe there reads back
	// consistently through the share while the host underneath flattens
	// everything to the invoking user, so it says yes and the build then
	// produces root-owned files and reports success. The differential caught
	// exactly that, one commit after it was introduced.
	//
	// A group is the fallback for an unprivileged process, where the uid probe
	// says only "you are not root" and the useful question is whether the
	// *filesystem* keeps what it is given. Any process may hand a file to a
	// group it belongs to.
	want, byUID := probeOwner(chown, probe)
	if !want.ok {
		// Nothing can be concluded. Allowed rather than refused: the copy
		// reports its own failure per file, and refusing on no evidence would
		// be the check inventing an answer.
		return nil
	}

	_ = byUID

	fi, err := os.Lstat(probe)
	if err != nil {
		return fmt.Errorf("--keep-own: cannot check whether %s carries ownership: %w", dir, err)
	}

	uid, gid, ok := ownerOf(fi)
	if !ok {
		return fmt.Errorf("--keep-own: %s does not report ownership on this platform", dir)
	}

	got := gid
	if byUID {
		got = uid
	}

	if got == want.id {
		return nil
	}

	// Named in full, because the cause is three layers away from the Earthfile
	// line that asked and nobody would find it: the flag is honoured inside the
	// step and lost when the layer is committed to a store the host filesystem
	// owns.
	return fmt.Errorf(
		"--keep-own: %s discards ownership - a file handed to uid %d came back as %d"+
			"\n  the layer store is a host directory shared into the sandbox, and a share"+
			"\n  whose host filesystem has no uids of its own cannot carry them: measured"+
			"\n  on macOS, a file a step made 65534:65534 is the invoking user in the store"+
			"\n  the flag works where the store is on a filesystem with real uids, which"+
			"\n  means a Linux host; refusing here rather than putting differently-owned"+
			"\n  files in the image and reporting success (green paper A2)",
		dir, want.id, got)
}

// storeOwnership answers the question once per process.
//
// Once because it is a filesystem property rather than a per-copy one, and a
// build with two hundred `--keep-own` copies must not write two hundred probe
// files into a store other builds are reading.
type storeOwnership struct {
	once sync.Once
	err  error
}

func (s *storeOwnership) check(dir string) error {
	s.once.Do(func() { s.err = checkStoreOwnership(dir, os.Lchown) })

	return s.err
}
