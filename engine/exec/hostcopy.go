package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// hostDir is where a LOCALLY step runs: the project directory, moved by WORKDIR.
//
// The project, because a LOCALLY target's commands are written relative to the
// Earthfile that contains them.
func (e *Executor) hostDir(n *ir.Node) (string, error) {
	dir := e.Context

	switch {
	case n.Op.HostAbs:
		// An absolute WORKDIR after LOCALLY, which is a directory on this
		// machine and not one under the project. See ir.Op.HostAbs.
		dir = filepath.Clean("/" + n.Op.Dir)

	case n.Op.Dir != "" && n.Op.Dir != "/":
		dir = filepath.Join(e.Context, filepath.Clean("/"+n.Op.Dir))
	}

	// Absolute, because it becomes PWD and a shell keeps a relative PWD as
	// given: a context of "." made `pwd` print ".".
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}

	// **A WORKDIR makes its directory**, on this machine as in a container: the
	// reference does, and `tests/local+test-locally-workdir` names a fresh one.
	if dir != e.Context {
		err := os.MkdirAll(dir, 0o755) //nolint:gosec // a directory the Earthfile named, as a shell would make it
		if err != nil {
			return "", fmt.Errorf("make the WORKDIR %s for %s: %w", dir, n.Meta.Source, err)
		}
	}

	return dir, nil
}

// hostCopy is `COPY +t/x dest` in a LOCALLY target: the artifact, put on this
// machine before the next step runs. See interp's localCopy for why it is a
// step and not an export.
//
// Staged first and placed second, because where it goes depends on what it
// is - a directory's contents, or a file into or onto dest - and that is known
// only once it is here.
func (e *Executor) hostCopy(ctx context.Context, n *ir.Node, sources [][]ir.NodeID) (core.Result, error) {
	if len(n.Op.Args) != 3 || len(sources) != 1 {
		return core.Result{}, fmt.Errorf("a LOCALLY COPY takes one artifact and a destination (%s)", n.Meta.Source)
	}

	// The stored path, and the name the reference asked for: `SAVE ARTIFACT
	// /etc/motd alpine-motd` keeps the bytes at one and calls them the other.
	path, dest, asked := n.Op.Args[0], n.Op.Args[1], n.Op.Args[2]

	dir, err := e.hostDir(n)
	if err != nil {
		return core.Result{}, err
	}

	if !filepath.IsAbs(dest) {
		dest = filepath.Join(dir, dest) + trailingSlash(dest)
	}

	tmp, err := os.MkdirTemp("", "earthbuild-locally-copy-*")
	if err != nil {
		return core.Result{}, fmt.Errorf("stage %s for %s: %w", path, n.Meta.Source, err)
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	// A pattern stages as a directory of its matches, which is then placed as
	// a directory's contents are.
	name := filepath.Base(asked)
	if isPattern(path) || name == "." || name == "/" {
		name = "matches"
	}

	staged := filepath.Join(tmp, name)

	err = e.ExportInternal(ctx, sources[0], path, staged, false)
	if err != nil {
		return core.Result{}, fmt.Errorf("COPY %s (%s): %w", path, n.Meta.Source, err)
	}

	err = placeHostCopy(staged, dest, n.Op.DirCopy)
	if err != nil {
		return core.Result{}, fmt.Errorf("COPY %s (%s): %w", path, n.Meta.Source, err)
	}

	// Nothing captured: a host step has no layer (I7).
	return core.Result{}, nil
}

// placeHostCopy puts a staged artifact where a container COPY would.
//
// A directory's contents go into dest, merging with what is there, unless
// dirCopy (`--dir`) asks for the directory itself. A file goes into dest when
// dest is a directory - ending in `/`, or already one - and replaces it
// otherwise.
func placeHostCopy(staged, dest string, dirCopy bool) error {
	fi, err := os.Lstat(staged)
	if err != nil {
		return fmt.Errorf("the artifact was not staged: %w", err)
	}

	into := strings.HasSuffix(dest, "/")
	dest = filepath.Clean(dest)

	if !into {
		existing, statErr := os.Stat(dest)
		into = statErr == nil && existing.IsDir()
	}

	switch {
	case fi.IsDir() && !dirCopy:
	case fi.IsDir() || into:
		dest = filepath.Join(dest, filepath.Base(staged))
	}

	return copyOut(staged, dest)
}

func trailingSlash(p string) string {
	if strings.HasSuffix(p, "/") {
		return "/"
	}

	return ""
}
