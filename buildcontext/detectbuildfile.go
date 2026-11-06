package buildcontext

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/domain"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// EarthfileNotExistError is returned when neither an Earthfile nor a build.earth
// file exists in the directory that a reference points to.
type EarthfileNotExistError struct {
	// Target is the reference being resolved, e.g. `+build` or `./sub+build`.
	Target string
	// Dir is the directory that was searched. For local references it is an
	// absolute path on the host; for remote references it is the path within the
	// repository.
	Dir string
	// Remote reports whether Dir is a path within a remote git repository.
	Remote bool
}

// Error implements [error] interface.
func (err EarthfileNotExistError) Error() string {
	var b strings.Builder

	b.WriteString("no " + Earthfile + " or " + buildEarthFile + " file found")

	if err.Dir != "" {
		if err.Remote {
			b.WriteString(" in repository directory " + err.Dir)
		} else {
			b.WriteString(" in " + err.Dir)
		}
	}

	if err.Target != "" {
		b.WriteString(" for target " + err.Target)
	}

	if !err.Remote {
		b.WriteString(" (run earth from the directory that contains the " + Earthfile +
			", or reference the target by its path, e.g. ./path/to/dir+target)")
	}

	return b.String()
}

// withTarget returns err with its EarthfileNotExistError (if any) stamped with
// the given reference. The build file lookup is cached per directory, so the
// cached error may have been produced while resolving a different target in
// the same directory; it is also wrapped with cache-internal context that is
// noise to the user. Both are dropped here.
func withTarget(err error, ref domain.Reference) error {
	notExist, ok := errors.AsType[EarthfileNotExistError](err)
	if !ok {
		return err
	}

	notExist.Target = ref.String()

	return notExist
}

// detectBuildFile detects whether to use Earthfile, build.earth or Dockerfile.
func detectBuildFile(ref domain.Reference, localDir string) (string, error) {
	if after, ok := strings.CutPrefix(ref.GetName(), DockerfileMetaTarget); ok {
		return filepath.Join(localDir, after), nil
	}

	earthfilePath := filepath.Join(localDir, Earthfile)

	_, err := os.Stat(earthfilePath)
	if os.IsNotExist(err) {
		buildEarthPath := filepath.Join(localDir, buildEarthFile)

		_, err = os.Stat(buildEarthPath)
		if os.IsNotExist(err) {
			return "", EarthfileNotExistError{Target: ref.String(), Dir: absOrSelf(localDir)}
		} else if err != nil {
			return "", fmt.Errorf("stat file %s: %w", buildEarthPath, err)
		}

		return buildEarthPath, nil
	} else if err != nil {
		return "", fmt.Errorf("stat file %s: %w", earthfilePath, err)
	}

	return earthfilePath, nil
}

// absOrSelf returns the absolute form of dir, or dir itself if that cannot be
// determined.
func absOrSelf(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}

	return abs
}

func detectBuildFileInRef(
	ctx context.Context, earthRef domain.Reference, ref gwclient.Reference, subDir string,
) (string, error) {
	if after, ok := strings.CutPrefix(earthRef.GetName(), DockerfileMetaTarget); ok {
		return filepath.Join(subDir, after), nil
	}

	earthfilePath := path.Join(subDir, Earthfile)

	exists, err := fileExists(ctx, ref, earthfilePath)
	if err != nil {
		return "", err
	}

	if exists {
		return earthfilePath, nil
	}

	buildEarthPath := path.Join(subDir, buildEarthFile)

	exists, err = fileExists(ctx, ref, buildEarthPath)
	if err != nil {
		return "", err
	}

	if exists {
		return buildEarthPath, nil
	}

	return "", EarthfileNotExistError{Target: earthRef.String(), Dir: subDir, Remote: true}
}

func fileExists(ctx context.Context, ref gwclient.Reference, fpath string) (bool, error) {
	dir, file := path.Split(fpath)

	fstats, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{
		Path:           dir,
		IncludePattern: file,
	})
	if err != nil {
		return false, fmt.Errorf("cannot read dir %s: %w", dir, err)
	}

	for _, fstat := range fstats {
		name := path.Base(fstat.GetPath())
		if name == file && !fstat.IsDir() {
			return true, nil
		}
	}

	return false, nil
}
