package exec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// Export writes an artifact from a step's filesystem to a path on the host.
//
// Two hops, and both are forced. The guest copies the artifact into the store
// they share, because the host cannot read the sandbox's filesystem; the host
// then copies it where the user asked, because the store is the engine's and
// not somewhere a user's `dist/` should live.
func (e *Executor) Export(ctx context.Context, stack []ir.NodeID, path, localDest string, ifExists, force bool) error {
	// The project the destination has to be inside. See insideProject: the
	// check is about a path the *Earthfile* named.
	project := e.Context

	// **`--force` is the caller saying where "outside" stops mattering**, which
	// is the state insideProject already has a word for: no project, no
	// containment. Said this way rather than with a second flag through the
	// export path, so there is one rule about outside-the-project writes and one
	// place that implements it.
	//
	// The permission was decided in the interpreter, which allows it for an
	// Earthfile this machine owns and never for a fetched one. By here it is a
	// decision already taken.
	if force {
		project = ""
	}

	return e.exportTo(ctx, stack, path, localDest, ifExists, project)
}

// ExportInternal writes an artifact to a directory the engine chose itself.
//
// The same work as Export without the project check, and the distinction is the
// point rather than a convenience. `insideProject` exists because `AS LOCAL` is
// the one command in the language that names a path on the machine running the
// build, and an Earthfile is routinely somebody else's code. **A destination the
// engine picked is not that**: no Earthfile named it, and nothing an Earthfile
// can write changes where it is.
//
// One caller: planning a `FROM DOCKERFILE +gen/` has to read the produced
// Dockerfile out of `+gen` into a temporary directory before the plan can exist
// (E488). That export was refused for writing outside a project it was never
// asked to write into, which is the check answering a question nobody asked
// (E490).
//
// Separate methods rather than a boolean, so the call site says which kind of
// destination it has: a flag would be read as "skip the check" and this is
// "there is nothing here for that check to be about".
func (e *Executor) ExportInternal(
	ctx context.Context, stack []ir.NodeID, path, dest string, ifExists bool,
) error {
	return e.exportTo(ctx, stack, path, dest, ifExists, "")
}

// exportTo is both of them: `project` empty means no destination check.
func (e *Executor) exportTo(
	ctx context.Context, stack []ir.NodeID, path, localDest string, ifExists bool,
	project string,
) error {
	if localDest == "" {
		// Produced but not exported. A legitimate artifact - another target may
		// reference it - so this is not an error.
		return nil
	}

	// Where this artifact was found last time. A stack is a list of
	// content-addressed layers, so the answer cannot go stale - only the file
	// can, and Lookup stats it. On a fully cached build this is the difference
	// between waking a sandbox and not: nothing else in the build needs one
	// (E569).
	memo := store.OpenExportMemo(e.sb.StoreDir())
	if guest.ShareExports() {
		if rel, ok := memo.Lookup(stack, path); ok {
			endMemo := phase("export:memo", path)
			defer endMemo()

			err := insideProject(project, localDest)
			if err != nil {
				return err
			}

			return copyOut(filepath.Join(e.sb.StoreDir(), rel), localDest)
		}
	}

	// Kept before the flatten below reassigns it, because the memo is about what
	// the caller asked for and a flattened stack is an implementation detail of
	// how it gets mounted.
	asked := stack

	endClient := phase("export:client", path)
	c, err := e.client()
	endClient()

	if err != nil {
		return err
	}

	// The same policy the scheduler applies to what a step runs on. A step's
	// stack is its base plus its own layer, so a build flattened to exactly the
	// limit is exported one layer over it - and the build has already
	// succeeded by the time that fails (E109).
	stack, squash := flattenForMount(stack)

	if squash != nil {
		endSquash := phase("export:squash", path)
		err = e.Squash(ctx, stack[0], squash)
		endSquash()

		if err != nil {
			return fmt.Errorf("collapse %d layers into one to read %s: %w", len(squash), path, err)
		}
	}

	endMat := phase("export:materialise", path)
	h, err := c.Materialise(ctx, stack)
	endMat()

	if err != nil {
		return fmt.Errorf("materialise the filesystem holding %s: %w", path, err)
	}

	defer func() {
		endRel := phase("export:release", path)
		_ = h.Release()
		endRel()
	}()

	// Named by destination so two artifacts cannot collide in the staging area,
	// and so a partial export is visible as the wrong file rather than as a
	// mysteriously absent one.
	// **A pattern stages under a name of its own**, because staging it under
	// the destination means the exports root for `AS LOCAL .` - see
	// stagingFor. The contents are copied out below, which is what makes the
	// destination a place rather than a name.
	stage := stagingFor(path, localDest)

	// `SAVE ARTIFACT --if-exists` means an absent path is not a failure. The
	// question travels with the export and is answered in the guest, because
	// the materialised root is a path in the guest's mount namespace: stat'ing
	// it from here failed whatever was there, so the flag skipped every save it
	// was ever applied to. The guest still answers it separately from the
	// export's own error, since "the file was not there" and "the export went
	// wrong" must not be the same answer - treating them alike would turn a
	// broken export into a silently skipped artifact.
	endStage := phase("export:stage", path)
	shared, absent, err := c.Export(ctx, h, path, stage, ifExists)
	endStage()

	if err != nil {
		return err
	}

	if absent {
		return nil
	}

	// Checked here, next to the write, and not only in the interpreter that
	// already refuses it. See insideProject.
	err = insideProject(project, localDest)
	if err != nil {
		return err
	}

	staged := filepath.Join(e.sb.StoreDir(), "exports", filepath.Clean("/"+stage))

	// Nothing was staged, because nothing needed to be: the guest recognised
	// the artifact as a file the store already holds, so the host reads it off
	// its own disk. Same bytes, same mode, same time - a published layer is
	// stamped when it is published (I8) - and one fewer 45 MB trip out of the
	// VM (E568).
	if shared != "" {
		staged = filepath.Join(e.sb.StoreDir(), filepath.Clean("/"+shared))

		memo.Note(asked, path, shared)
	}

	endOut := phase("export:copyout", localDest)
	defer endOut()

	return copyOut(staged, localDest)
}

// copyOut moves a staged artifact to where the user asked for it.
// stagingFor is where the guest puts what it exported, before it is copied to
// the destination on this machine.
//
// The destination itself for a single artifact, which is what every other part
// of export assumes. **A pattern gets a directory of its own**, and this is the
// whole of the fix: staged under the destination, `SAVE ARTIFACT /output/* AS
// LOCAL .` stages into `exports/.` - the exports *root*, holding every artifact
// this store has ever staged - and copying that out wrote thirteen unrelated
// files from other tests into the project.
//
// Named from the request rather than randomly, so a build repeated does not
// leave one directory per invocation, and two patterns to one destination do
// not share.
func stagingFor(path, localDest string) string {
	if !strings.ContainsAny(filepath.Base(path), "*?[") {
		return localDest
	}

	sum := sha256.Sum256([]byte(path + "\x00" + localDest))

	return filepath.Join(".patterns", hex.EncodeToString(sum[:8]))
}

func copyOut(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("the guest did not stage %s: %w", dst, err)
	}

	err = os.MkdirAll(filepath.Dir(dst), 0o750)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}

	return copyOutKnown(src, dst, fi)
}

// copyOutKnown is copyOut for a caller that has already lstatted the source and
// created the destination's parent.
//
// **Both were being done twice per file.** `filepath.Walk` lstats every entry
// and hands the result to its callback, which threw it away; and the walk
// creates each directory as it descends, so the `MkdirAll` before every file was
// asking about a directory it had just made. Two syscalls of about six, on a
// path a large context pays once per file: staging measured 78.5us a file
// against 31.7 for `cp -r` (E878).
func copyOutKnown(src, dst string, fi os.FileInfo) error {
	// The caller's lstat is used, not a fresh stat. `copyDir` walks with
	// `filepath.Walk`, which lstats: a symlink is not a directory to it, so the
	// entry arrives here - and stat followed the link, saw a directory, and
	// walked it. A link to its own parent then closed the circle and the engine
	// died with `fatal error: stack overflow` on a `GIT CLONE` checkout that had
	// one (E452).
	//
	// **Two functions disagreeing about what a symlink is.** Answering the same
	// way as the walk that called us is what makes the pair terminate, which is
	// why this takes the walk's own answer rather than asking again.
	//
	// A link arrives as a link, which is also what a layer holds (the rule
	// `SAVE ARTIFACT --symlink-no-follow` is a no-op against) - so an export and
	// a capture describe the same tree.
	if fi.Mode()&os.ModeSymlink != 0 {
		return copyLink(src, dst)
	}

	if fi.IsDir() {
		return copyDir(src, dst)
	}

	// **The filesystem's copy where it can make one.** An artifact is the
	// largest thing a build hands back - 45MB for this repository's own binary
	// - and reading it into memory to write it out again was half a second of a
	// 1.6s build that had nothing else to do (E566). A clone shares the extents
	// and diverges on the first write, which is what a caller of a copy expects
	// and what makes it safe for a file the user then edits.
	//
	// The times are still set below: a clone carries the source's, and the
	// source is a staged copy or the store's layer itself, so the stamp is what
	// decides what the artifact ends up with either way.
	if mayClone() && cloneOneFile(src, dst) {
		return stampOut(dst, fi)
	}

	b, err := os.ReadFile(src) //nolint:gosec // our own staging directory
	if err != nil {
		return fmt.Errorf("read the staged artifact: %w", err)
	}

	return placeOut(dst, b, fi)
}

// placeOut writes an artifact beside its destination and renames it over.
//
// **Because a build often replaces the program that is running it.** CI builds
// `build/linux/amd64/earthly` with the copy of it that is executing, and
// opening that path for writing fails with ETXTBSY - "text file busy" - which
// is not a fault in the Earthfile and nothing the build can act on. A rename
// replaces the *name*: the running program keeps the inode it started from and
// the next execution gets the new one, which is how every package manager on
// the machine replaces a binary in use.
//
// Atomic as a consequence, which is worth as much. A reader sees the old file
// or the new one and never half of either, and a build interrupted midway
// leaves the previous artifact rather than a truncated one (E760).
//
// dst is checked by insideProject before this is reached, which resolves
// symlinks on the nearest existing ancestor; the temporary lands in that same
// directory, because a rename cannot cross a filesystem.
func placeOut(dst string, b []byte, fi os.FileInfo) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+"-")
	if err != nil {
		return fmt.Errorf("stage %s beside its destination: %w", dst, err)
	}

	staged := tmp.Name()

	undo := func(because error) error {
		_ = tmp.Close()
		_ = os.Remove(staged)

		return because
	}

	_, err = tmp.Write(b)
	if err != nil {
		return undo(fmt.Errorf("write %s: %w", dst, err))
	}

	err = tmp.Close()
	if err != nil {
		return undo(fmt.Errorf("write %s: %w", dst, err))
	}

	// Before the rename, so the artifact never exists at its own name with the
	// wrong mode: CreateTemp makes it 0600, and an executable arriving
	// unreadable is a worse failure than one arriving a moment later.
	err = os.Chmod(staged, fi.Mode())
	if err != nil {
		return undo(fmt.Errorf("set the mode of %s: %w", dst, err))
	}

	// Preserved because an artifact's mtime is part of what it is: a build tool
	// that stamps every output with the current time defeats every downstream
	// tool that compares timestamps, which is most of them (I8). Stamped before
	// the rename for the same reason as the mode.
	err = stampOut(staged, fi)
	if err != nil {
		return undo(err)
	}

	err = os.Rename(staged, dst)
	if err != nil {
		return undo(fmt.Errorf("put %s in place: %w", dst, err))
	}

	return nil
}

// stampOut gives an exported artifact the time it should carry.
//
// Preserved because an artifact's mtime is part of what it is: a build tool that
// stamps every output with the current time defeats every downstream tool that
// compares timestamps, which is most of them (I8). The clamp when the
// invocation asked for one, the file's own otherwise - see clamp.go for why the
// engine takes the instruction rather than choosing.
//
// Shared by the copy and the clone, so the two cannot disagree about what an
// artifact's time is. They did not, and a second implementation is how they
// would start.
func stampOut(dst string, fi os.FileInfo) error {
	at := stamp(fi.ModTime())

	err := os.Chtimes(dst, at, at)
	if err != nil {
		return fmt.Errorf("set the mtime on %s: %w", dst, err)
	}

	return nil
}

func copyDir(src, dst string) error {
	return copyDirExcluding(src, dst, nil)
}

// excluder is what decides a path is left out of a copy.
//
// An interface rather than the concrete type so that a copy with nothing to
// exclude passes nil and reads as "no opinion" rather than "an empty one".
type excluder interface{ Excludes(rel string) bool }

// copyDirExcluding copies a tree, leaving out what an excluder names.
//
// **The context's bytes and the context's identity have to agree.** The digest
// is taken with the ignore file applied and this copied everything, so
// `.earthlyignore` decided the cache key and did not decide what the container
// got - a context whose contents do not match its own identity (E623).
//
// The cost was visible as well as wrong: this repository generates about sixty
// thousand fixture files into gitignored `testdata/`, every one of them named by
// `.earthlyignore`, and every native build copied all of them.
func copyDirExcluding(src, dst string, ex excluder) error {
	// Directory modes are applied once everything is in place, deepest first,
	// for the two reasons the guest's copyTree gives - and this side needed them
	// just as much, which nothing noticed because the export tests compare
	// files. A directory the build left unwritable cannot be *filled* after it
	// is created at its own mode, so the copy failed outright; and os.MkdirAll
	// passes its mode through the umask, so one that could be created arrived
	// with a mode nobody asked for.
	modes := map[string]os.FileMode{}

	walked := filepath.Walk(src, func(p string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}

		if ex != nil && rel != "." && ex.Excludes(filepath.ToSlash(rel)) {
			// A directory that is excluded takes its contents with it, which is
			// the whole saving: descending into a twenty-thousand-file fixture
			// to reject each file individually costs what it was meant to avoid.
			if fi.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		target := filepath.Join(dst, rel)

		if fi.IsDir() {
			modes[target] = fi.Mode() &
				(os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)

			// Writable while it is being filled; the mode it keeps is set
			// below, after its contents are in.
			err := os.MkdirAll(target, 0o700)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}

			return nil
		}

		return copyOutKnown(p, target, fi)
	})
	if walked != nil {
		return walked
	}

	return applyDirModes(modes)
}

// applyDirModes sets the collected directory modes, deepest first.
//
// Deepest first so a directory is never made unwritable before the one beneath
// it has been given its own mode, and by chmod rather than by the mode passed
// to MkdirAll, which the umask filters: 0777 asked for under the usual 022
// arrives as 0755, and a mode that is a request is not a mode.
func applyDirModes(modes map[string]os.FileMode) error {
	paths := make([]string, 0, len(modes))
	for p := range modes {
		paths = append(paths, p)
	}

	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], string(os.PathSeparator)) >
			strings.Count(paths[j], string(os.PathSeparator))
	})

	for _, p := range paths {
		err := os.Chmod(p, modes[p])
		if err != nil {
			return fmt.Errorf("set the mode on %s: %w", p, err)
		}
	}

	return nil
}

// insideProject refuses a destination that would write outside the project.
//
// The second check on `SAVE ARTIFACT ... AS LOCAL`, and deliberately not the
// only one: the interpreter already refuses an absolute path, a `~`, and a
// destination that climbs out with `..`. This is the layer that would do the
// damage, which is where the check earns its keep - the same arrangement, for
// the same reason, as `within()` in front of the git fetcher's RemoveAll.
//
// It matters because an Earthfile is routinely somebody else's code, fetched
// from somewhere else, and `AS LOCAL` is the one command in the language that
// names a path on the machine running the build.
//
// Resolved rather than cleaned: `sub/../../sibling` does not look like an
// escape and is one, and a symlink in the middle of the path is an escape the
// string cannot show at all.
func insideProject(root, dest string) error {
	// No project means the caller has not said what "outside" is. Inventing an
	// answer would refuse exports a library user arranged for themselves.
	if root == "" {
		return nil
	}

	base, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve the project directory: %w", err)
	}

	at := dest
	if !filepath.IsAbs(at) {
		at = filepath.Join(base, at)
	}

	at, err = filepath.Abs(at)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", dest, err)
	}

	// The nearest existing ancestor is resolved, because the destination itself
	// usually does not exist yet - that is the point of writing it - and
	// EvalSymlinks on a path that is not there answers nothing.
	// `resolved`, not `real`: that is a built-in function name, and redefining
	// one is the sort of thing a reader has to notice rather than read past.
	resolved, err := filepath.EvalSymlinks(existingAncestor(at))
	if err == nil {
		realBase, err := filepath.EvalSymlinks(base)
		if err == nil {
			base = realBase
		}

		at = filepath.Join(resolved, strings.TrimPrefix(at, existingAncestor(at)))
	}

	if at != base && !strings.HasPrefix(at, base+string(filepath.Separator)) {
		return fmt.Errorf(
			"SAVE ARTIFACT AS LOCAL %q would write outside the project"+
				"\n  it resolves to %s, and this build writes under %s",
			dest, at, base)
	}

	return nil
}

// existingAncestor is the closest directory on a path that exists.
func existingAncestor(p string) string {
	for at := p; ; at = filepath.Dir(at) {
		_, err := os.Stat(at)
		if err == nil {
			return at
		}

		if parent := filepath.Dir(at); parent == at {
			return at
		}
	}
}

// flattenForMount shortens a stack to what mount(2) will accept, and names the
// range that has to be squashed to make that true.
//
// Φ (green paper 4.8), at the second place that mounts a stack. The scheduler
// applies it to a step's base; this applies it to the step's whole stack, which
// is one layer deeper. A nil range means the stack already fitted.
//
// The oldest layers are the ones collapsed, for the reason `core.Flatten` gives:
// edits land near the top, so granularity is worth most there and the base is
// what stays unchanged between builds.
func flattenForMount(stack []ir.NodeID) (mount []ir.NodeID, squash []ir.NodeID) {
	out, flat := core.Flatten(stack, store.MountableStackDepth, core.SquashID)
	if !flat.Applied() {
		return out, nil
	}

	return out, stack[flat.From:flat.To]
}

// copyLink reproduces a symlink rather than what it points at.
//
// Replaced rather than skipped when something is already there: an export runs
// over a destination a previous build wrote, and `os.Symlink` fails on an
// existing name.
func copyLink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return fmt.Errorf("read the link %s: %w", src, err)
	}

	err = os.Remove(dst)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace %s: %w", dst, err)
	}

	err = os.Symlink(target, dst)
	if err != nil {
		return fmt.Errorf("write the link %s: %w", dst, err)
	}

	return nil
}
