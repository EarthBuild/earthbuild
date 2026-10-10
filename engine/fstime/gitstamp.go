package fstime

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// epoch is the fixed stamp for a path with no honest time.
//
// Not the Unix epoch itself: some tools treat a zero time as "unset" and
// substitute the current one, which would put the clock back in by the very
// mechanism meant to keep it out.
var epoch = time.Unix(1, 0).UTC()

// commitMark separates a commit's time from the paths that follow it.
//
// `--format=%ct` alone is ambiguous under `-z`: a file named `1700000000` is a
// token indistinguishable from a timestamp. A byte no path carries removes the
// guess.
const commitMark = "\x01"

// FromHistory is the time each path in a checkout should carry, or nil where
// there is no history to ask.
//
// **Why history rather than the filesystem.** A packed context is a layer, and
// a layer's identity is its bytes, so a timestamp read off disk makes two clones
// of one commit build different layers - which is why every entry was pinned to
// a fixed epoch instead. But a flat tree is one an incremental compiler cannot
// read: cargo compares each source's mtime against the fingerprint in `target/`
// and rebuilds what is strictly newer, so with every source at the epoch either
// nothing is ever fresh or an edit is silently ignored. Both were measured, the
// second leaving a stale binary.
//
// A commit time is the quantity that satisfies both. It is a property of the
// history rather than of the clone, so two machines agree on it; and it only
// ever moves forward, so it carries the ordering that content alone cannot.
//
// **Uncommitted edits get the local clock, and lose nothing by it.** A modified
// working tree is not reproducible by definition - nobody else has those bytes -
// so there is no shared answer to forgo, and the local mtime is exactly what the
// compiler needs to see. Reproducibility is kept for the case where it can exist
// and spent where it cannot.
//
// Cost is one `git log` walk, abandoned as soon as every wanted path has a time:
// 0.46s over 4836 commits and 3138 files when nothing lets it stop early.
func FromHistory(ctx context.Context, root string, names []string) func(rel string) time.Time {
	prefix, ok := gitLine(ctx, root, "rev-parse", "--show-prefix")
	if !ok {
		return nil
	}

	// Nil means every tracked path under this root, which is what a caller
	// wants when it intends to keep the answer: one walk then covers every
	// question a build can ask, and no caller has to record which paths it has
	// already asked about.
	if names == nil {
		tracked, listed := gitOut(ctx, root, "ls-files", "--full-name", "-z")
		if !listed {
			return nil
		}

		for at := range pathsIn(bytes.NewReader(tracked)) {
			names = append(names, strings.TrimPrefix(at, prefix))
		}
	}

	// **The status, not `diff-index`.** `diff-index` answers from stat data, so
	// a file merely touched reads as modified - and would be handed the local
	// clock, losing the one property a commit time was chosen for. The status
	// compares content, and names the modified and the untracked in one answer.
	//
	// `--no-optional-locks` because this is a question, not a change: refreshing
	// the stat cache is a write, and a build tool should not take the index lock
	// out from under whatever the developer is doing in the next terminal.
	//
	// Paths come back relative to the top of the repository, which `ls-files`
	// does not do unless told - a context rooted at the top of a checkout is the
	// one case where the two agree, and the first test of this was written
	// there, so the disagreement was invisible.
	status, gitOK := gitOut(ctx, root,
		"--no-optional-locks", "status", "--porcelain=v1", "-z",
		"--untracked-files=all", "--no-renames", "--ignored=no")
	if !gitOK {
		return nil
	}

	dirty := dirtyIn(bytes.NewReader(status))

	// Only clean, tracked paths are worth walking history for, and asking about
	// fewer of them is what lets the walk stop early.
	want := map[string]bool{}

	for _, rel := range names {
		at := prefix + rel
		if !dirty[at] {
			want[at] = true
		}
	}

	history := map[string]time.Time{}

	if len(want) > 0 {
		cmd, out, started := gitStream(ctx, root, "log",
			"--format="+commitMark+"%ct", "--name-only", "--no-renames", "-z", "HEAD")
		if !started {
			return nil
		}

		for at, when := range commitTimesIn(out, want) {
			history[strings.TrimPrefix(at, prefix)] = when
		}

		endStream(cmd, out)
	}

	local := map[string]bool{}
	for at := range dirty {
		local[strings.TrimPrefix(at, prefix)] = true
	}

	return chooseStamp(history, local, diskTime(root))
}

// chooseStamp is the three-way choice, kept apart from the four processes that
// have to run before it can be made.
func chooseStamp(
	history map[string]time.Time,
	dirty map[string]bool,
	onDisk diskClock,
) func(rel string) time.Time {
	return func(rel string) time.Time {
		if when, ok := history[rel]; ok && !dirty[rel] {
			return when
		}

		if when, ok := onDisk(rel); ok {
			return when
		}

		return epoch
	}
}

// diskTime reads the local clock, and declines to for a directory.
//
// A directory's mtime says when its listing last changed, which no compiler
// consults and which two checkouts never agree on - so carrying it would spend
// the determinism this exists to keep, and buy nothing. Directories stay at the
// epoch, exactly as they always were.
// diskClock says what the filesystem holds for a path, and whether it holds
// anything worth carrying.
type diskClock func(rel string) (time.Time, bool)

func diskTime(root string) diskClock {
	return func(rel string) (time.Time, bool) {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || info.IsDir() {
			return time.Time{}, false
		}

		return info.ModTime(), true
	}
}

// commitTimesIn reads the log stream, newest commit first, and gives each wanted
// path the first time it is seen under.
//
// It returns as soon as every wanted path has one. The tail of a history says
// nothing about the tree being packed, and on a large repository it is most of
// the walk.
func commitTimesIn(r io.Reader, want map[string]bool) map[string]time.Time {
	found := map[string]time.Time{}
	when := epoch

	scan := bufio.NewScanner(r)
	scan.Split(splitNul)

	for scan.Scan() {
		tok := scan.Text()

		if mark, isCommit := strings.CutPrefix(tok, commitMark); isCommit {
			sec, err := strconv.ParseInt(strings.TrimSpace(mark), 10, 64)
			if err != nil {
				return found
			}

			when = time.Unix(sec, 0)

			continue
		}

		// The first path of each commit carries the newline the format left
		// behind it.
		at := strings.TrimPrefix(tok, "\n")
		if at == "" || !want[at] {
			continue
		}

		if _, seen := found[at]; !seen {
			found[at] = when

			if len(found) == len(want) {
				return found
			}
		}
	}

	return found
}

// dirtyIn reads `git status --porcelain -z`: a two-letter state, a space, and
// the path.
//
// Every state counts. Modified, added, deleted, untracked and conflicted all
// mean the same thing here - the working tree is not what the commit says, so
// there is no shared answer to carry and the local clock is the honest one.
func dirtyIn(r io.Reader) map[string]bool {
	const code = 3 // "XY ", ahead of the path

	dirty := map[string]bool{}

	scan := bufio.NewScanner(r)
	scan.Split(splitNul)

	for scan.Scan() {
		// Shorter than a state and a path cannot be either, and nothing here can
		// tell a truncated record from a file whose name is a status code.
		if rec := scan.Text(); len(rec) > code {
			dirty[rec[code:]] = true
		}
	}

	return dirty
}

// pathsIn reads a plain NUL-separated list of paths.
func pathsIn(r io.Reader) map[string]bool {
	paths := map[string]bool{}

	scan := bufio.NewScanner(r)
	scan.Split(splitNul)

	for scan.Scan() {
		if at := scan.Text(); at != "" {
			paths[at] = true
		}
	}

	return paths
}

// splitNul is bufio.ScanLines with the separator git uses when it refuses to
// quote: the one byte a path cannot contain.
func splitNul(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}

	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}

	return 0, nil, nil
}

func gitOut(ctx context.Context, root string, args ...string) ([]byte, bool) {
	//nolint:gosec // every argument here is a literal
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Stderr = nil

	out, err := cmd.Output()

	return out, err == nil
}

func gitLine(ctx context.Context, root string, args ...string) (string, bool) {
	out, ok := gitOut(ctx, root, args...)

	return strings.TrimSpace(string(out)), ok
}

// gitStream starts a walk this reader may abandon part-way, so the pipe is
// closed and the process waited on rather than left to fill a buffer nobody
// drains.
func gitStream(ctx context.Context, root string, args ...string) (*exec.Cmd, io.ReadCloser, bool) {
	//nolint:gosec // every argument here is a literal
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)

	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, false
	}

	err = cmd.Start()
	if err != nil {
		return nil, nil, false
	}

	return cmd, out, true
}

func endStream(cmd *exec.Cmd, out io.ReadCloser) {
	_ = out.Close()
	_ = cmd.Wait()
}

// Head is the commit a stamping was built from, or "" where there is none.
//
// A memo over `FromHistory` is only good while this is unchanged: an executor
// outlives a build in a worker, and times carried over from a history the files
// are no longer in would be wrong in the one way nothing downstream can detect.
func Head(ctx context.Context, root string) string {
	at, _ := gitLine(ctx, root, "rev-parse", "HEAD")

	return at
}
