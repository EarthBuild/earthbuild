package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
func (e *Executor) Export(ctx context.Context, stack []ir.NodeID, path, localDest string, ifExists bool) error {
	// The project the destination has to be inside. See insideProject: the
	// check is about a path the *Earthfile* named.
	return e.exportTo(ctx, stack, path, localDest, ifExists, e.Context)
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

	// `SAVE ARTIFACT --if-exists` means an absent path is not a failure. Asked
	// of the materialised filesystem rather than inferred from an export error,
	// because "the file was not there" and "the export went wrong" must not be
	// the same answer - treating them alike would turn a broken export into a
	// silently skipped artifact.
	if ifExists {
		_, err := os.Stat(filepath.Join(h.Root(), filepath.Clean("/"+path)))
		if err != nil {
			return nil
		}
	}

	// Named by destination so two artifacts cannot collide in the staging area,
	// and so a partial export is visible as the wrong file rather than as a
	// mysteriously absent one.
	endStage := phase("export:stage", path)
	shared, err := c.Export(ctx, h, path, localDest)
	endStage()

	if err != nil {
		return err
	}

	// Checked here, next to the write, and not only in the interpreter that
	// already refuses it. See insideProject.
	err = insideProject(project, localDest)
	if err != nil {
		return err
	}

	staged := filepath.Join(e.sb.StoreDir(), "exports", filepath.Clean("/"+localDest))

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
func copyOut(src, dst string) error {
	// Lstat, not Stat. `copyDir` walks with `filepath.Walk`, which lstats: a
	// symlink is not a directory to it, so the entry arrives here - and stat
	// followed the link, saw a directory, and walked it. A link to its own
	// parent then closed the circle and the engine died with `fatal error: stack
	// overflow` on a `GIT CLONE` checkout that had one (E452).
	//
	// **Two functions disagreeing about what a symlink is.** Answering the same
	// way as the walk that called us is what makes the pair terminate.
	fi, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("the guest did not stage %s: %w", dst, err)
	}

	// A link arrives as a link, which is also what a layer holds (the rule
	// `SAVE ARTIFACT --symlink-no-follow` is a no-op against) - so an export and
	// a capture describe the same tree.
	if fi.Mode()&os.ModeSymlink != 0 {
		return copyLink(src, dst)
	}

	err = os.MkdirAll(filepath.Dir(dst), 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
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

	// dst is checked by insideProject immediately above, which resolves
	// symlinks on the nearest existing ancestor - the taint is real and the
	// guard is what answers it. TestASymlinkCannotBeUsedToEscapeTheProject
	// fails if that check is removed, so this suppression is one somebody
	// keeps true rather than one they have to remember.
	err = os.WriteFile(dst, b, fi.Mode()) //nolint:gosec // guarded by insideProject, above
	if err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}

	// Preserved because an artifact's mtime is part of what it is: a build tool
	// that stamps every output with the current time defeats every downstream
	// tool that compares timestamps, which is most of them (I8).
	// The clamp when the invocation asked for one, the file's own time
	// otherwise. See clampTime: pinning timestamps and keeping them true are
	// both right, for different builds, so the engine takes the instruction
	// rather than choosing.
	return stampOut(dst, fi)
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
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}

		target := filepath.Join(dst, rel)

		if fi.IsDir() {
			err := os.MkdirAll(target, fi.Mode())
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}

			return nil
		}

		return copyOut(p, target)
	})
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
		if _, err := os.Stat(at); err == nil {
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
