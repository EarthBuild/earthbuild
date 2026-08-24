//go:build linux

package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// waitFor blocks until a path the sandbox provides exists.
//
// The docker daemon creates its socket several seconds after the VM boots, and
// the first build to want one arrives before it. Waiting is the honest fix:
// the socket is coming, and failing because it has not arrived yet would make a
// build succeed or fail on how long the machine took to start.
//
// Only the first build after a boot waits at all. The VM outlives a build, so
// every later one finds the daemon already up and this returns immediately.
//
// Bounded, because a path that will never appear must not hang a build - the
// diagnosis at the end is the same one a missing path would have given, since
// waiting a minute for it does not change what is wrong.
func waitFor(path string) error {
	const (
		limit = 90 * time.Second
		poll  = 50 * time.Millisecond
	)

	deadline := time.Now().Add(limit)

	for {
		_, err := os.Stat(path)
		if err == nil {
			return nil
		}

		// Late, or absent? A daemon creates its socket inside a directory that
		// is already there; nothing conjures the directory. So a path whose
		// parent is missing is not coming, and waiting ninety seconds to learn
		// that produces the diagnosis the first stat could have given.
		//
		// `/usr/local/bin/docker` is the case: a binary in an image, and the
		// layers are mounted before the step runs. Across the corpus that was
		// eleven `WITH DOCKER` targets at a minute and a half each.
		//
		// Being wrong here costs a fast refusal instead of a slow one, with the
		// same message. That is the direction to be wrong in.
		_, dirErr := os.Stat(filepath.Dir(path))
		if dirErr != nil {
			return fmt.Errorf(
				"the sandbox has no %s to give this step"+
					"\n  %s"+
					"\n  a WITH DOCKER block needs a sandbox image with a daemon in it,"+
					" and that daemon has to be running",
				path, explainMissing(path))
		}

		if time.Now().After(deadline) {
			// What is there instead, because the three causes of this - the
			// image has no docker in it, nothing was mounted, the store was
			// never attached - have three different remedies and used to share
			// one sentence. E28 has five failures nobody could attribute.
			return fmt.Errorf(
				"the sandbox has no %s to give this step, after waiting %s"+
					"\n  %s"+
					"\n  a WITH DOCKER block needs a sandbox image with a daemon in it,"+
					" and that daemon has to be running",
				path, limit, explainMissing(path))
		}

		time.Sleep(poll)
	}
}

// bindMounts makes directories visible inside a step's filesystem.
//
// A mount is not a layer, and the difference is the whole point. A layer is
// stacked and becomes part of what the step produces; a mount is a hole in that
// filesystem onto something that outlives the step. `CACHE /root/.m2` wants the
// second: a compiler's cache that survives to the next build and is *not* part
// of the image.
//
// Bound before the chroot, because the source is a path the guest can name and
// the target is a path inside a root that does not exist yet as far as the
// process is concerned. Afterwards there would be no way to reach the source.
func bindMounts(root, store string, mounts []Mount) (undo func(), err error) {
	var (
		done   []string
		staged []string
		// persisted are [inside the step, on this machine] pairs copied back
		// when the step is over.
		persisted [][2]string
	)

	// created are mount points this engine made because nothing was there. They
	// are removed again after the unmount: a mount point is not something the
	// step produced.
	var created []string

	// touched are directories this engine made a mount point in, with what they
	// held and when, from before it did.
	//
	// Removing the mount point is not enough. Adding an entry to a directory
	// changes that directory's mtime and removing it changes it again, so a
	// parent the engine only passed through comes out carrying the moment the
	// step started - and overlayfs has copied it up into the delta by then.
	// `/etc` and `/dev`, empty, were what remained of E547 and were enough to
	// give `RUN true` a different identity on every machine (E548).
	var touched []directoryAsFound

	unmount := func() {
		// Reverse order: a mount inside another has to go first, and the list is
		// applied outermost-first.
		for i := len(done) - 1; i >= 0; i-- {
			unmountAll(done[i])
		}

		// Copied back before anything is removed, so what the step added to a
		// persisted cache survives to the next build. Errors are dropped: the
		// step has already run and succeeded, and failing it now for a cache
		// that could not be written back would discard work that was done.
		for _, pair := range persisted {
			_ = copyTree(pair[0], pair[1], copyOpts{})
		}

		// Removed after the unmount, so a credential does not outlive the step
		// that was given it.
		for _, dir := range staged {
			_ = os.RemoveAll(dir)
		}

		// A mount point this engine created is taken away again, so it does not
		// end up in the step's layer.
		//
		// A mount is a hole: what was under it stays as it was, and what the
		// step wrote into it is not part of what the step produced. A directory
		// made only so there was something to bind onto is ours, not the
		// step's, and leaving it behind put an empty `/cache` in the image
		// where the reference engine puts nothing at all (E33).
		//
		// Deepest first, and only when empty - `os.Remove` on a non-empty
		// directory fails, which is exactly the guard wanted: a mount point the
		// image already had keeps whatever the image put in it.
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.Remove(created[i])
		}

		// After the removals, because what is being asked is whether the
		// directory ends as it began.
		for _, d := range touched {
			d.restore()
		}
	}

	for _, m := range mounts {
		// Four ways a mount can say where its contents come from: a directory in
		// the store, a path on this machine, a directory made for this step, or
		// the contents themselves. A secret has always been the fourth and
		// carried an id as well; the step's `/etc/hosts` is the first mount to
		// be only its contents, which is what found this condition rejecting it.
		if (m.ID == "" && m.Sandbox == "" && !m.Ephemeral && m.Secret == "") || m.Target == "" {
			unmount()

			return nil, fmt.Errorf(
				"a mount needs an id, a sandbox path, contents or ephemeral, and a target")
		}

		source := filepath.Join(store, m.ID)

		// A sandbox path is the machine's own, not the store's: the docker
		// client and its socket, which belong to the VM and outlive the step.
		// It must already exist - creating it would make an empty directory
		// where a socket was expected and the failure would appear inside the
		// step as a daemon that is not answering.
		if m.Sandbox != "" {
			source = m.Sandbox

			err := waitFor(source)
			if err != nil {
				unmount()

				return nil, err
			}
		}

		// An ephemeral mount is a directory made for this step and removed with
		// it, staged the same way a secret is and for the same reason: what a
		// step writes into its own root is captured, and this must not be
		// (E398). It differs from a secret only in being a directory and in
		// having nothing put in it.
		if m.Ephemeral {
			dir, err := os.MkdirTemp("", "earthbuild-step-*")
			if err != nil {
				unmount()

				return nil, fmt.Errorf("make a directory for this step: %w", err)
			}

			// MkdirTemp makes it 0700, which is right for a directory nobody
			// else may enter and wrong for one the step asked to be 0777.
			err = applyMode(dir, m, 0o755)
			if err != nil {
				unmount()

				return nil, err
			}

			source = dir
			staged = append(staged, dir)
		}

		// A secret is written outside the step's filesystem and bound in, for
		// the reason a cache is: what a step writes into its own root is
		// captured, and a credential written there would be in the image. The
		// file is created with no group or other access and removed when the
		// step is done.
		if m.Secret != "" {
			dir, err := os.MkdirTemp("", "earthbuild-secret-*")
			if err != nil {
				unmount()

				return nil, fmt.Errorf("stage a secret: %w", err)
			}

			source = filepath.Join(dir, "secret")

			err = os.WriteFile(source, []byte(m.Secret), 0o400)
			if err != nil {
				_ = os.RemoveAll(dir)
				unmount()

				return nil, fmt.Errorf("stage a secret: %w", err)
			}

			// The mode the Earthfile asked for, on the *source*. Setting it on
			// the mount point is what the code did and it cannot be seen: a
			// bind shows the source's inode, so the step reads the source's
			// mode and the mount point's is hidden underneath (E435).
			err = applyMode(source, m, 0o400)
			if err != nil {
				_ = os.RemoveAll(dir)
				unmount()

				return nil, err
			}

			staged = append(staged, dir)
		}

		// The source must exist before it can be bound, and a cache mount names
		// a directory that is empty on the first build by definition.
		if m.Secret == "" && m.Sandbox == "" && !m.Ephemeral {
			//nolint:gosec // a mount point carries the mode the mount asked for
			err := os.MkdirAll(source, 0o755)
			if err != nil {
				unmount()

				return nil, fmt.Errorf("prepare the mount source %s: %w", source, err)
			}

			err = applyMode(source, m, 0o755)
			if err != nil {
				unmount()

				return nil, err
			}
		}

		target, err := within(root, m.Target)
		if err != nil {
			unmount()

			return nil, err
		}

		// A file source needs a file to land on, and a directory a directory.
		// A secret is always a file; a sandbox path may be either, so it is
		// asked rather than assumed.
		asFile := m.Secret != ""
		if m.Sandbox != "" {
			fi, statErr := os.Stat(source)
			asFile = statErr == nil && !fi.IsDir()
		}

		if asFile {
			// A file, because the source is one: bind-mounting a file onto a
			// directory fails, and a step asking for a secret at a path expects
			// to read it there.
			perm := os.FileMode(0o400)
			if m.Mode != 0 {
				perm = os.FileMode(m.Mode)
			}

			// Asked before creating it, exactly as the directory branch does
			// and for the same reason: afterwards there is no way to tell
			// whether the file was the image's or ours.
			//
			// This branch did not ask, so a file mount point was never taken
			// away - and every step captured the sandbox's plumbing as its own
			// output: `/etc/resolv.conf` and six device nodes in the delta of a
			// step that writes nothing, each stamped with the moment the step
			// started, which made `RUN true` produce a different layer every
			// run (E547).
			_, bad := os.Lstat(target)
			missing := bad != nil

			if missing {
				found, ok := findDirectory(filepath.Dir(target))
				if ok {
					touched = append(touched, found)
				}
			}

			err := ensureFile(target, perm)
			if err != nil {
				unmount()

				return nil, fmt.Errorf("prepare the mount point %s: %w", m.Target, err)
			}

			if missing {
				created = append(created, target)
			}
		} else {
			// Whether the directory was already there decides whether it is
			// ours to remove afterwards. Asked before creating it, because
			// afterwards there is no way to tell.
			_, statErr := os.Lstat(target)
			missing := statErr != nil

			//nolint:gosec // a mount point carries the mode the mount asked for
			statErr = os.MkdirAll(target, 0o755)
			if statErr != nil {
				unmount()

				return nil, fmt.Errorf("prepare the mount point %s: %w", m.Target, statErr)
			}

			if missing {
				created = append(created, target)
			}
		}

		// A persisted cache is copied rather than bound, because a bind is
		// invisible to the capture: what a step writes into it never reaches the
		// overlay's upper layer. Copying in puts the contents where the capture
		// will find them, and copying out at the end keeps them for next time.
		if m.Persist {
			// The repository's own copyTree, which preserves mtimes because they
			// are part of a layer's identity (I8) - a second implementation here
			// would have reset them and produced a layer whose digest did not
			// match the one just computed.
			err := copyTree(source, target, copyOpts{})
			if err != nil {
				unmount()

				return nil, fmt.Errorf("restore the cache at %s: %w", m.Target, err)
			}

			persisted = append(persisted, [2]string{target, source})

			continue
		}

		err = unix.Mount(source, target, "", unix.MS_BIND|unix.MS_REC, "")
		if err != nil {
			unmount()

			return nil, fmt.Errorf("mount %s at %s: %w", source, m.Target, err)
		}

		done = append(done, target)

		if !m.ReadOnly {
			continue
		}

		// Read-only needs a second call: the flag is ignored on the bind itself,
		// which is a kernel behaviour that silently produces a writable mount if
		// you assume otherwise.
		//
		// And it must carry the flags the mount already has. Inside a user
		// namespace the kernel *locks* the flags a mount inherited - nodev,
		// nosuid, noexec - and refuses a remount that would clear them, which
		// is what omitting them does. Running rootless, this failed with
		// `make /etc/resolv.conf read-only: operation not permitted` while
		// asking for nothing but read-only.
		flags := unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY | lockedFlags(target)

		err = unix.Mount("", target, "", uintptr(flags), "")
		if err != nil {
			unmount()

			return nil, fmt.Errorf("make %s read-only: %w", m.Target, err)
		}
	}

	return unmount, nil
}

// mountProc puts a proc filesystem in a step's root.
//
// Not a convenience. The dynamic loader computes `$ORIGIN` from
// /proc/self/exe, so a binary whose rpath uses it - which is every JDK, and a
// great many toolchains - fails with "cannot open shared object file" naming a
// library that is present, readable, and resolvable by `ldd`. That is a
// diagnosis pointing at the wrong thing entirely, and `maven:3.8.5-openjdk-17`
// could not run java at all.
//
// A fresh proc rather than a bind of the sandbox's, so the step sees its own
// processes and not the guest's - which would be ambient state a step could
// observe and no key describes (I3).
func mountProc(root string) (undo func(), err error) {
	target := filepath.Join(root, "proc")

	//nolint:gosec // a mount point carries the mode the mount asked for
	err = os.MkdirAll(target, 0o755)
	if err != nil {
		return nil, fmt.Errorf("make room for /proc: %w", err)
	}

	err = unix.Mount("proc", target, "proc", 0, "")
	if err != nil {
		return nil, fmt.Errorf("mount /proc for the step: %w", err)
	}

	return func() { unmountAll(target) }, nil
}

// resolverMount gives a step the machine's resolver configuration.
//
// An image ships no /etc/resolv.conf, because the runtime is expected to
// provide one - and nothing did, so DNS did not work in any step at all. Every
// build that fetches anything resolves a name first, so maven, npm, pip, apt
// and cargo all failed, each with its own unrelated-looking error.
//
// Bound from the sandbox rather than written here: what the resolver should be
// is the machine's business, and inventing a nameserver would be guessing at
// somebody's network.
//
// It is ambient state, and worth being explicit about: a step that reads it
// observes something no key describes. So does every step that reaches the
// network, which is why RUN is what it is - this changes nothing about what is
// cacheable, it only makes a step that was going to fetch able to.
func resolverMount() []Mount {
	const path = "/etc/resolv.conf"

	_, err := os.Stat(path)
	if err != nil {
		return nil
	}

	return []Mount{{Sandbox: path, Target: path, ReadOnly: true, Mode: 0o644}}
}

// deviceMounts are the device files every step is entitled to.
//
// A step's filesystem is its layer stack, and a layer stack contains no
// devices: an image ships an empty /dev because the runtime is expected to
// populate it. Nothing did, so a step had /dev/null and nothing else - and
// /dev/urandom is what every language runtime, every TLS handshake and most
// package managers reach for first. The symptom that led here was smaller and
// stranger: docker's plugin loader opens /dev/null while collecting metadata,
// and `docker compose` reported itself as an unknown command.
//
// Bound from the sandbox rather than created with mknod, because a bind needs
// no privileges this already has and gives exactly the machine's own devices -
// and because the alternative is a list of major and minor numbers, which is a
// list to get wrong.
//
// Absent ones are skipped rather than demanded. A sandbox image is entitled to
// differ, and a build must not stop because the machine has no /dev/full.
func deviceMounts() []Mount {
	const mode = 0o666

	var out []Mount

	for _, dev := range []string{
		"/dev/null", "/dev/zero", "/dev/full",
		"/dev/random", "/dev/urandom", "/dev/tty",
	} {
		_, err := os.Stat(dev)
		if err != nil {
			continue
		}

		out = append(out, Mount{Sandbox: dev, Target: dev, Mode: mode})
	}

	return out
}

// lockedFlags are the mount options a remount must preserve.
//
// A user namespace locks whatever its parent had set - nodev, nosuid, noexec,
// and the time-tracking pair - and rejects a remount that drops one. Outside a
// namespace they are already set on the mount, so re-asserting them changes
// nothing; inside one, omitting them is EPERM.
//
// Read from the mount itself rather than assumed, because which are locked
// depends on how the machine mounted the filesystem underneath.
func lockedFlags(target string) int {
	var st unix.Statfs_t

	err := unix.Statfs(target, &st)
	if err != nil {
		return 0
	}

	return mountFlagsOf(int(st.Flags))
}

// mountFlagsOf turns statfs flags into the mount flags a remount must repeat.
//
// Separate from the statfs call so the mapping can be tested with the pairs
// that matter rather than with whatever this machine's filesystems happen to
// report.
//
// **MS_NOATIME and MS_RELATIME are mutually exclusive**, and a remount carrying
// both is refused with EINVAL. statfs reports them independently and can set
// both, so repeating them verbatim asks the kernel for something it will not
// give. noatime wins: a mount that never updates access times already satisfies
// anything relatime would have asked for.
//
// This is the flake in E171a - one full Linux run in four, never in isolation,
// because which filesystem `/etc/resolv.conf` sits on decides which atime bits
// appear. An intermittent failure whose cause is a *pair* of flags reads
// exactly like a race and is not one (E172).
func mountFlagsOf(statfsFlags int) int {
	var out int

	for _, f := range []struct{ statfs, mount int }{
		{unix.ST_NODEV, unix.MS_NODEV},
		{unix.ST_NOSUID, unix.MS_NOSUID},
		{unix.ST_NOEXEC, unix.MS_NOEXEC},
		{unix.ST_NOATIME, unix.MS_NOATIME},
		{unix.ST_NODIRATIME, unix.MS_NODIRATIME},
		{unix.ST_RELATIME, unix.MS_RELATIME},
	} {
		if statfsFlags&f.statfs != 0 {
			out |= f.mount
		}
	}

	if out&unix.MS_NOATIME != 0 {
		out &^= unix.MS_RELATIME
	}

	return out
}

// unmountAll pops every mount stacked at a path.
//
// **A bind mount is a stack, not a flag.** Two steps sharing one materialised
// root each bind `/etc/resolv.conf` over the same target, and a single
// `Unmount` removes one of the two - so the path stays busy forever and the
// root can never be removed. A long-running guest then accumulates one mount
// per concurrent step, until it reaches the kernel's limit.
//
// Found by tests that had never run: every test in this package that roots a
// step in a temporary directory was skipping on Linux, and running as root
// inside a VM on macOS where nothing removed the directory afterwards (E122).
// Three concurrent subtests over one handle was all it took.
//
// Bounded, because a target that will not come free must not spin: the loop
// stops as soon as unmounting fails, which is the normal ending - EINVAL when
// nothing is mounted there any more.
func unmountAll(target string) {
	const most = 32

	for range most {
		err := unix.Unmount(target, unix.MNT_DETACH)
		if err != nil {
			return
		}
	}
}

// applyMode puts the mount's requested permissions on what will be bound.
//
// On the source, because that is what the step sees: a bind shows the source's
// inode, and a mode set on the mount point is underneath it. Nothing to do when
// the Earthfile asked for nothing - `MkdirAll` and `WriteFile` already used the
// default, and chmod-ing to the same value would be a syscall that can fail for
// no reason.
func applyMode(path string, m Mount, deflt os.FileMode) error {
	if m.Mode == 0 || os.FileMode(m.Mode).Perm() == deflt {
		return nil
	}

	err := os.Chmod(path, os.FileMode(m.Mode).Perm())
	if err != nil {
		return fmt.Errorf("set mode %#o on %s: %w", m.Mode, m.Target, err)
	}

	return nil
}
