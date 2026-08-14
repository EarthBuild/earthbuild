package buildcontext

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/domain"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// EarthfileNotExistError is the struct indicating that file does not exist.
type EarthfileNotExistError struct {
	Target string
}

// Error implements [error] interface.
func (err EarthfileNotExistError) Error() string {
	return "No Earthfile nor build.earth file found for target " + err.Target
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
			return "", EarthfileNotExistError{Target: ref.String()}
		} else if err != nil {
			return "", fmt.Errorf("stat file %s: %w", buildEarthPath, err)
		}

		return buildEarthPath, nil
	} else if err != nil {
		return "", fmt.Errorf("stat file %s: %w", earthfilePath, err)
	}

	return earthfilePath, nil
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

	return "", EarthfileNotExistError{Target: earthRef.String()}
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
