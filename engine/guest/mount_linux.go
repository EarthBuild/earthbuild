//go:build linux

package guest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/timing"
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
func bindMounts(root, store, layers, delta string, mounts []Mount) (undo func(), err error) {
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
		// Teardown is the mirror of the bind and costs about as much, which was
		// invisible until it was split: it runs in a defer, so the host's `run`
		// phase covered it and nothing else did.
		defer timing.Phase("guest:unbind", "")()

		endDetach := timing.Phase("guest:unbind:umount", count(len(done)))

		// Reverse order: a mount inside another has to go first, and the list is
		// applied outermost-first.
		for i := range slices.Backward(done) {
			unmountAll(done[i])
		}

		endDetach()

		// Copied back before anything is removed, so what the step added to a
		// persisted cache survives to the next build. Errors are dropped: the
		// step has already run and succeeded, and failing it now for a cache
		// that could not be written back would discard work that was done.
		endPersist := timing.Phase("guest:unbind:persist", count(len(persisted)))

		for _, pair := range persisted {
			_ = copyTree(pair[0], pair[1], copyOpts{})
		}

		endPersist()

		// Removed after the unmount, so a credential does not outlive the step
		// that was given it.
		endStaged := timing.Phase("guest:unbind:staged", count(len(staged)))

		for _, dir := range staged {
			_ = os.RemoveAll(dir)
		}

		endStaged()

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
		endCreated := timing.Phase("guest:unbind:created", count(len(created)))

		removeCreated(created)

		endCreated()

		// After the removals, because what is being asked is whether the
		// directory ends as it began.
		endTouched := timing.Phase("guest:unbind:touched", count(len(touched)))

		for _, d := range touched {
			d.restore()
		}

		endTouched()
	}

	for _, m := range mounts {
		// Four ways a mount can say where its contents come from: a directory in
		// the store, a path on this machine, a directory made for this step, or
		// the contents themselves. A secret has always been the fourth and
		// carried an id as well; the step's `/etc/hosts` is the first mount to
		// be only its contents, which is what found this condition rejecting it.
		if (m.ID == "" && m.Sandbox == "" && m.Layer == "" && !m.Ephemeral && m.Secret == "") || m.Target == "" {
			unmount()

			return nil, errors.New(
				"a mount needs an id, a sandbox path, contents or ephemeral, and a target")
		}

		source := filepath.Join(store, m.ID)

		// A bound view resolves against the *layer* store, which is a different
		// directory from the cache store above. Read-only is not a courtesy
		// here: the layer store is shared by every step that stands on it, and
		// a step writing through this would edit another step's input - the one
		// thing a content-addressed store cannot survive (§3.3b, I20).
		if m.Layer != "" {
			source = filepath.Join(layers, "layers", m.Layer)
			if m.Sub != "" {
				source = filepath.Join(source, m.Sub)
			}
		}

		// A sandbox path is the machine's own, not the store's: the docker
		// client and its socket, which belong to the VM and outlive the step.
		// It must already exist - creating it would make an empty directory
		// where a socket was expected and the failure would appear inside the
		// step as a daemon that is not answering.
		if m.Sandbox != "" {
			source = sandboxSource(m.Sandbox, layers)

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
			dir, err := ephemeralDir(m.ID)
			if err != nil {
				unmount()

				return nil, err
			}

			// **`--mount=type=tmpfs` is memory, and the difference is the
			// point.** An ephemeral directory already disappears with the step,
			// so a disk one satisfies every promise the construct makes except
			// the one worth having: what a step writes here must not reach a
			// filesystem it could be recovered from.
			if m.Tmpfs {
				err = unix.Mount("tmpfs", dir, "tmpfs", 0, "")
				if err != nil {
					unmount()

					return nil, fmt.Errorf("mount a tmpfs for this step at %s: %w", m.Target, err)
				}
			}

			// MkdirTemp makes it 0700, which is right for a directory nobody
			// else may enter and wrong for one the step asked to be 0777.
			err = applyMode(dir, m, 0o755)
			if err != nil {
				unmount()

				return nil, err
			}

			source = dir
			// **A shared one is not this step's to remove.** An ephemeral mount
			// without an id belongs to the step and goes with it; one with an id
			// is the block's, and the next step in that block has to find what
			// this one left. It goes when the sandbox does, which is the
			// lifetime a block actually has (E886).
			if m.ID == "" {
				staged = append(staged, dir)
			}
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
				found, ok := findDirectory(filepath.Dir(target), deltaOf(root, delta, filepath.Dir(target)))
				if ok {
					touched = append(touched, found)
				}
			}

			ensureErr := ensureFile(target, perm)
			if ensureErr != nil {
				unmount()

				return nil, fmt.Errorf("prepare the mount point %s: %w", m.Target, ensureErr)
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
			copyErr := copyTree(source, target, copyOpts{})
			if copyErr != nil {
				unmount()

				return nil, fmt.Errorf("restore the cache at %s: %w", m.Target, copyErr)
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

// mountSys puts a sysfs in a step's root, if this machine will allow one.
//
// **What reads it.** Every runtime that asks how big the machine is: the JVM's
// container awareness, Go's cgroup-aware `GOMAXPROCS`, `nproc`, and anything
// enumerating block devices or interfaces. They read `/sys/fs/cgroup` and
// `/sys/devices/system/cpu`, and where those are absent they do not fail - they
// answer with the *host's* numbers, or with one CPU, which is a wrong answer
// delivered confidently. `earth-entrypoint.sh` branches on
// `/sys/fs/cgroup/cgroup.controllers` to tell cgroups v2 from v1, so a nested
// build reads the absence as v1 (E753).
//
// Read-only, as an OCI runtime mounts it: a step has no business writing to the
// machine's device tree, and the one thing that legitimately wants a writable
// path under here - a nested runtime making cgroups - needs a cgroup2 mount
// rather than a writable sysfs.
//
// **Degraded rather than refused, unlike /proc.** Mounting sysfs needs the
// network namespace to belong to the user namespace doing the mounting, which
// is true for a guest running as root and false for a rootless one sharing the
// machine's network. A step without /sys is worse than a step with it and far
// better than no step at all, so this reports and continues - the rule cgroups
// already follow, and the opposite of the rule /proc follows, because a JDK
// cannot start at all without /proc/self/exe.
func mountSys(root string) (undo func(), why error) {
	return mountSysWith(root, unix.Mount)
}

// mountSysWith is mountSys with the mount call supplied, so the refusal that
// matters can be forced rather than waited for.
//
// **The bind is not a lesser /sys, it is the same one.** sysfs is
// network-namespace tagged, and this engine deliberately does not apply
// CLONE_NEWNET (see isolationFlags), so a step shares the guest's network
// namespace and a fresh sysfs mount would show exactly what a bind of the
// guest's own /sys shows. What differs is only what the kernel asks for:
// instantiating a sysfs superblock requires the mounting user namespace to own
// the network namespace, and binding an existing mount requires nothing of the
// sort.
//
// That distinction is the whole of this. Every Native CI job reported
// `mount /sys for the step: operation not permitted`, three times each, because
// a GitHub runner is exactly the case the comment above predicted - and a
// developer's privileged container is exactly the case that hides it. Without
// /sys there is nowhere to put /sys/fs/cgroup, so the inner runtime found no
// cgroup mount and started nothing (E839a).
//
// Read-only either way: a step has no business writing to the machine's sysfs,
// and MS_BIND does not carry flags, so the remount asserts them.
//
// **Recursive, and then blanked - both measured on the kernel rather than
// reasoned about.** A shallow bind of /sys is refused in a user namespace with
// EINVAL, because it would expose files hidden by submounts; the recursive form
// succeeds. So MS_REC is not a choice.
//
// It brings the machine's cgroup2 with it - 85 entries where a fresh sysfs
// shows an empty directory - and that cannot be unmounted, because mounts
// inherited when a user namespace was created are locked.
//
// **Blanking it with a tmpfs was tried and would have made things worse.** On a
// runner, mountCgroup2 fails too - `operation not permitted` - so the machine's
// tree arriving with the bind is the only cgroup mount a step gets, and a
// nested runtime finds one solely because of it. Covering it restored I3 and
// put `no cgroup mount found in mountinfo` straight back (E841a).
//
// So this leaves it, and the guest reports that it did: what a step should see
// when its own cgroup tree cannot be mounted is a decision about ambient state
// (I3), not something to settle inside a mount helper.
func mountSysWith(root string, mount mountFunc) (undo func(), why error) {
	target := filepath.Join(root, "sys")

	//nolint:gosec // a mount point carries the mode the mount asked for
	err := os.MkdirAll(target, 0o755)
	if err != nil {
		return func() {}, fmt.Errorf("make room for /sys: %w", err)
	}

	const flags = unix.MS_RDONLY | unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC

	fresh := mount("sysfs", target, "sysfs", flags, "")
	if fresh == nil {
		return func() { unmountAll(target) }, nil
	}

	bound := mount("/sys", target, "none", unix.MS_BIND|unix.MS_REC, "")
	if bound == nil {

		// A bind takes the source's flags, so read-only is asserted afterwards.
		// Failing that is not failing the mount: a step with a writable /sys is
		// worse than one with a read-only /sys and much better than one with
		// none, which is the rule this whole function follows.
		_ = mount("", target, "none", unix.MS_BIND|unix.MS_REMOUNT|flags, "")

		return func() { unmountAll(target) }, nil
	}

	// Both reasons, because either alone sends a reader to the wrong half of
	// this: the first says the namespace does not permit a new sysfs, and the
	// second says the machine's own could not be shown instead.
	return func() {}, fmt.Errorf(
		"mount /sys for the step: %w\n  and binding the machine's own instead: %w"+
			"\n  a step without one is told the machine"+
			"\n  has one CPU and no cgroup limits, rather than being told nothing",
		fresh, bound)
}

// mountFunc is unix.Mount, named so it can be supplied.
type mountFunc func(source, target, fstype string, flags uintptr, data string) error

// mountCgroup2 puts the step's own cgroup tree at /sys/fs/cgroup.
//
// **Why a step wants one.** `earth-entrypoint.sh` tells cgroups v2 from v1 by
// looking for `/sys/fs/cgroup/cgroup.controllers`, and a nested runtime -
// buildkitd, dockerd, runc - makes cgroups for what it starts. Absent, the
// entrypoint reads v2 as v1 and configures a daemon for a machine that is not
// there, which is how `connect provided buildkit: timeout` was arrived at
// (E754).
//
// **Why it is safe to give.** The step is in a cgroup namespace of its own, so
// what it mounts here is rooted at *its* cgroup and it cannot see or touch the
// machine's tree - the same arrangement a container runtime uses for
// `--privileged` with `cgroupns=private`. Without the namespace this would hand
// a step the machine's whole hierarchy, so the two go together and this refuses
// to mount if the namespace is not there.
//
// Skipped where the machine is on cgroups v1, whose layout is a directory per
// controller and whose delegation rules are not these. Nothing here needs to
// work on v1: it is a fallback for a nested build, and a nested build on a v1
// machine has the same problem this engine does.
func mountCgroup2(root string) (undo func(), why error) {
	target := filepath.Join(root, "sys", "fs", "cgroup")

	// A v2 machine has this file at the root of the unified hierarchy. Asked of
	// the machine rather than of the step, which has not got one yet.
	_, err := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		return func() {}, fmt.Errorf("this machine is not on cgroups v2: %w", err)
	}

	//nolint:gosec // a mount point carries the mode the mount asked for
	err = os.MkdirAll(target, 0o755)
	if err != nil {
		return func() {}, fmt.Errorf("make room for /sys/fs/cgroup: %w", err)
	}

	err = unix.Mount("cgroup2", target, "cgroup2", unix.MS_NOSUID|unix.MS_NODEV|
		unix.MS_NOEXEC, "")
	if err != nil {
		return func() {}, fmt.Errorf("mount /sys/fs/cgroup for the step: %w", err)
	}

	return func() { unmountAll(target) }, nil
}

// linkStdio makes the four names a shell expects to find in /dev.
//
// **The one that fails silently.** `/dev` here is a tmpfs this engine mounts,
// and a tmpfs starts empty. Without `/dev/stdin` and `/dev/fd`, `< /dev/stdin`
// and process substitution fail with a message naming the path - legible, if
// unwelcome. Without `/dev/stdout`, `echo … > /dev/stdout` does not fail at
// all: the shell creates a *regular file* called `stdout` in the tmpfs, writes
// to it, and the tmpfs goes away with the step. The output is gone and nothing
// said so, which is the worst way for a build to be wrong (E756).
//
// Symlinks into /proc/self/fd, which is what every runtime provides and what
// makes them work: they resolve per process, so the step's own descriptors are
// what they name. They are made inside the tmpfs, so they vanish with the step
// and reach no layer.
//
// An existing name is left as it is. Some images ship their own, and a step
// must not fail to start over a link that is already there.
func linkStdio(root string) error {
	for _, l := range []struct{ at, to string }{
		{"fd", "/proc/self/fd"},
		{"stdin", "/proc/self/fd/0"},
		{"stdout", "/proc/self/fd/1"},
		{"stderr", "/proc/self/fd/2"},
	} {
		at := filepath.Join(root, "dev", l.at)

		err := os.Symlink(l.to, at)
		if err == nil || errors.Is(err, fs.ErrExist) {
			continue
		}

		return fmt.Errorf("link /dev/%s to %s: %w", l.at, l.to, err)
	}

	return nil
}

// mountDevPts gives a step a pty of its own to allocate.
//
// Without it `openpty` has nothing to open: /dev is a tmpfs this engine makes
// and there is no /dev/ptmx in it, so `script`, `expect`, `docker run -t`, tmux
// and anything testing its own behaviour on a terminal fail with "failed to
// create pseudo-terminal" (E757).
//
// `newinstance`, so the ptys are this step's and not the machine's: two steps
// allocating at once must not be handed each other's, and a step must not see
// terminals belonging to whatever else is on the box - which would be ambient
// state no key describes (I3).
//
// `gid=5` is the tty group, which is the convention every image's `tty` binary
// expects. A user namespace that has not mapped that group cannot set it, and
// the kernel says EINVAL rather than ignoring it, so the mount is tried again
// without: a step whose terminals are owned by the wrong group works, and a
// step with no terminals at all does not.
//
// Skipped where it cannot be mounted, on the rule /sys follows: a step without
// a pty is worse than one with and far better than no step.
func mountDevPts(root string) (undo func(), why error) {
	target := filepath.Join(root, "dev", "pts")

	//nolint:gosec // a mount point carries the mode the mount asked for
	err := os.MkdirAll(target, 0o755)
	if err != nil {
		return func() {}, fmt.Errorf("make room for /dev/pts: %w", err)
	}

	const flags = unix.MS_NOSUID | unix.MS_NOEXEC

	err = unix.Mount("devpts", target, "devpts", flags,
		"newinstance,ptmxmode=0666,mode=0620,gid=5")
	if err != nil {
		err = unix.Mount("devpts", target, "devpts", flags,
			"newinstance,ptmxmode=0666,mode=0620")
	}

	if err != nil {
		return func() {}, fmt.Errorf("mount /dev/pts for the step: %w", err)
	}

	// Relative, and to this instance's own ptmx rather than the machine's:
	// opening /dev/ptmx has to allocate from the instance mounted above, which
	// is the whole point of `newinstance`.
	err = os.Symlink("pts/ptmx", filepath.Join(root, "dev", "ptmx"))
	if err != nil && !errors.Is(err, fs.ErrExist) {
		unmountAll(target)

		return func() {}, fmt.Errorf("link /dev/ptmx to this step's pts: %w", err)
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

// stepDevices are the device files every step is entitled to, named once so
// that what is bound and what is reported missing cannot drift apart.
var stepDevices = []string{
	"/dev/null", "/dev/zero", "/dev/full",
	"/dev/random", "/dev/urandom", "/dev/tty",
}

// missingDevices names the devices this sandbox does not have, relative to root.
//
// deviceMounts skips an absent device on purpose - a sandbox image is entitled
// to differ, and a build must not stop because the machine has no /dev/full -
// but nothing said which were skipped. A step without /dev/null does not report
// that: it reports whatever reached for it, and in CI that was `line 53: can't
// create /dev/null: Permission denied`, followed by an entrypoint concluding
// from the failed redirect that the container was unprivileged. Two wrong
// diagnoses, from one silence (E845).
//
// Rooted rather than absolute so a test can supply a directory instead of a
// machine; the guest passes "/".
func missingDevices(root string) []string {
	var out []string

	for _, dev := range stepDevices {
		_, err := os.Stat(filepath.Join(root, dev))
		if err != nil {
			out = append(out, dev)
		}
	}

	return out
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

	// **A directory of their own, first.** A bind needs a file to land on, and
	// creating one inside the step's merged overlay makes overlayfs materialise
	// the parent directory in upper, which means reading it through every lower
	// layer. Six devices bound straight into the overlay therefore cost time
	// proportional to how deep the build already is, on every step - which
	// makes a build quadratic in its own length (E635, E636).
	//
	// This mount costs nothing: /dev is already there, so nothing is created.
	// The six below land in it rather than in the overlay, and on twenty steps
	// that took binding from 31.7ms a step to 17.4ms (E637).
	//
	// First, because `bindMounts` works the list in order: a /dev arriving
	// later would be mounted over the devices already beneath it.
	out := []Mount{
		{Ephemeral: true, Target: "/dev", Mode: 0o755},
		// **Shared memory, which is a mount and not a device.** POSIX shared
		// memory is a file in a tmpfs at this path and nowhere else, so a step
		// without one has no `sem_open`, no `shm_open` and no
		// `multiprocessing`. The OCI runtime specification mounts it, so every
		// other engine a build has run under provided it, and its absence is
		// reported by whatever reached for it rather than as a missing mount:
		// Python says a semaphore does not exist, Chrome dies on its first tab,
		// PostgreSQL will not start (E752).
		//
		// Second, so it lands inside the /dev above rather than being hidden by
		// it. 1777 as everywhere else - world-writable, and sticky so one user
		// cannot remove another's segment.
		{Ephemeral: true, Tmpfs: true, Target: "/dev/shm", Mode: 0o1777},
	}

	for _, dev := range stepDevices {
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
// modeOf is a mount's mode as a FileMode, sticky bit and all.
//
// `os.FileMode.Perm()` masks to the low nine bits and Go spells the sticky bit
// outside them, so a mount asking for 1777 was chmodded to 0777 in silence.
// /dev/shm is 1777 on every machine a build has run on, and the difference does
// not show up until one user removes another's segment (E752).
func modeOf(mode uint32) os.FileMode {
	out := os.FileMode(mode).Perm()

	if mode&unix.S_ISVTX != 0 {
		out |= os.ModeSticky
	}

	return out
}

func applyMode(path string, m Mount, deflt os.FileMode) error {
	if m.Mode == 0 || modeOf(m.Mode) == deflt {
		return nil
	}

	err := os.Chmod(path, modeOf(m.Mode))
	if err != nil {
		return fmt.Errorf("set mode %#o on %s: %w", m.Mode, m.Target, err)
	}

	return nil
}

// count labels a phase with how many things it was for.
//
// **A duration with no denominator cannot be acted on.** `guest:unbind:created`
// reads 7ms a step, which is either a handful of removes over a share with a
// round trip each or a great many cheap ones - opposite conclusions, and the
// phase said nothing either way. Naming the count makes the per-item cost
// visible without a second experiment.
func count(n int) string {
	return strconv.Itoa(n)
}

// hostnameMount is the `/etc/hostname` a step gets.
//
// **Because the kernel's answer and the file's answer are both read.** E758 set
// the name in the step's UTS namespace, which is what `hostname` and `uname -n`
// report; `/etc/hostname` was left as whatever the image shipped - `localhost`
// in alpine's case - so the two disagreed and which one a tool believed was a
// property of the tool. Init scripts, JVM startup and a good deal of packaging
// read the file (E765).
//
// Always, and shadowing the image's, which is what a container runtime does and
// what `resolverMount` already does for `/etc/resolv.conf`: the image's copy is
// a leftover from whoever built it and describes a machine that no longer
// exists. A mount rather than a written file, so it is not captured into the
// step's layer.
//
// 0644 explicitly. A step running as a non-root user that cannot read its own
// machine name is a stranger failure than not having one.
func hostnameMount() []Mount {
	return []Mount{{Target: "/etc/hostname", Secret: SandboxHost + "\n", Mode: 0o644}}
}

// hostsMountFor is the `/etc/hosts` mount, on the platform that has mounts.
func hostsMountFor(entries []string) []Mount { return hostsMount(entries) }

// ephemeralScratch is where storage shared by one block lives.
//
// The guest's own filesystem rather than the store: what goes here must not be
// captured into a layer and must not outlive the sandbox, and this directory
// satisfies both by being neither.
//
// **Asked for, not assumed.** The two backends put it in different places -
// `/var/lib/earthbuild/scratch` inside the VM, `<root>/scratch` for a namespace
// - and hardcoding either gives the other `make the shared directory: no such
// file or directory` at the first `WITH DOCKER`. The temporary directory is the
// fallback rather than an error, because a block that shares within one process
// is still better than one that shares nothing.
func ephemeralScratch() string {
	if root := os.Getenv("EARTH_GUEST_SCRATCH"); root != "" {
		return filepath.Join(root, "scope")
	}

	return filepath.Join(os.TempDir(), "earthbuild-scope")
}

// ephemeralDir is the directory an ephemeral mount uses.
//
// **Named or not, and the difference is a lifetime.** Without an id it is this
// step's alone and goes when the step does, which is what `--mount=type=tmpfs`
// and a lone `WITH DOCKER` want. With one it is shared by every step carrying
// the same id and goes when the sandbox does, which is what a `WITH DOCKER`
// block wants: `--load` runs as one step and the body as another, and an image
// loaded by the first has to be there for the second (E886).
//
// **The id is a name, not a path.** It arrives in a step assignment from a peer
// this guest did not write (A5), so `../../..` in it would put daemon storage
// somewhere of the sender's choosing. Rejected rather than cleaned: a name that
// needed cleaning was not a name.
func ephemeralDir(id string) (string, error) {
	if id == "" {
		dir, err := os.MkdirTemp("", "earthbuild-step-*")
		if err != nil {
			return "", fmt.Errorf("make a directory for this step: %w", err)
		}

		return dir, nil
	}

	if !plainName(id) {
		return "", fmt.Errorf(
			"a shared ephemeral mount is named %q, which is not a name"+
				"\n  it becomes a directory inside this sandbox, so it may hold"+
				" letters, digits, dashes, underscores and one separator", id)
	}

	dir := filepath.Join(ephemeralScratch(), id)

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return "", fmt.Errorf("make the shared directory %s: %w", dir, err)
	}

	return dir, nil
}

// plainName reports whether an id is safe to join onto a path.
//
// One separator is allowed because the callers compose `docker-scope/<id>`, and
// a rule that forbade it would push the check somewhere it is easier to forget.
func plainName(id string) bool {
	if id == "" || strings.HasPrefix(id, "/") || strings.HasSuffix(id, "/") {
		return false
	}

	if strings.Count(id, "/") > 1 {
		return false
	}

	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/':
		default:
			return false
		}
	}

	// A dot is allowed in a name and `..` is not a name.
	return !strings.Contains(id, "..")
}
