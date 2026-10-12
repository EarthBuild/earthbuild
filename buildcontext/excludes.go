package buildcontext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/moby/patternmatcher/ignorefile"
)

const (
	earthIgnoreFile   = ".earthignore"
	earthlyIgnoreFile = ".earthlyignore"
	dockerIgnoreFile  = ".dockerignore"
)

const (
	tmpOutputDir          = ".tmp-earth-out"
	tmpOutputDirWithSlash = tmpOutputDir + "/"
)

const (
	buildEarthFile = "build.earth"

	// Earthfile is the default project configuration file.
	Earthfile = "Earthfile"
)

// secretFile is the default name of the file earth reads build secrets from
// (see the --secret-file-path flag). It must never be sent to buildkit as part
// of a local build context.
const secretFile = ".secret"

var errDuplicateIgnoreFile = errors.New("both .earthignore and .earthlyignore exist - please remove one")

// ImplicitExcludes is a list of implicit patterns to exclude.
var ImplicitExcludes = []string{
	tmpOutputDirWithSlash,
	buildEarthFile,
	Earthfile,
	earthIgnoreFile,
	earthlyIgnoreFile,
}

// SecretExcludes is a list of patterns that are excluded from every local build
// context, even when implicit ignore rules are disabled via the
// --no-implicit-ignore feature (which is enabled from VERSION 0.6 onwards).
// They protect secret files from being swept into the context (and therefore
// potentially into image layers) by e.g. FROM DOCKERFILE or COPY . ./.
// Use the --no-implicit-secret-ignore feature to opt out.
var SecretExcludes = []string{
	"**/" + secretFile,
}

// excludeOpts controls which implicit exclude patterns readExcludes applies.
type excludeOpts struct {
	// noImplicitIgnore disables ImplicitExcludes.
	noImplicitIgnore bool
	// noImplicitSecretIgnore disables SecretExcludes.
	noImplicitSecretIgnore bool
	// useDockerIgnore falls back to .dockerignore when neither .earthignore nor
	// .earthlyignore exist.
	useDockerIgnore bool
}

func readExcludes(dir string, opts excludeOpts) ([]string, error) {
	ignoreFile := earthIgnoreFile

	// earthIgnoreFile
	earthIgnoreFilePath := filepath.Join(dir, earthIgnoreFile)

	earthExists, err := fileutil.FileExists(earthIgnoreFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to check if %s exists: %w", earthIgnoreFilePath, err)
	}

	// earthlyIgnoreFile
	earthlyIgnoreFilePath := filepath.Join(dir, earthlyIgnoreFile)

	earthlyExists, err := fileutil.FileExists(earthlyIgnoreFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to check if %s exists: %w", earthlyIgnoreFilePath, err)
	}

	// dockerIgnoreFile
	dockerIgnoreFilePath := filepath.Join(dir, dockerIgnoreFile)

	dockerExists := false
	if opts.useDockerIgnore {
		dockerExists, err = fileutil.FileExists(dockerIgnoreFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to check if %s exists: %w", dockerIgnoreFilePath, err)
		}
	}

	defaultExcludes := []string{}
	if !opts.noImplicitIgnore {
		defaultExcludes = append(defaultExcludes, ImplicitExcludes...)
	}

	if !opts.noImplicitSecretIgnore {
		defaultExcludes = append(defaultExcludes, SecretExcludes...)
	}

	// Check which ones exists and which don't
	if earthExists && earthlyExists {
		// if both exist then throw an error
		return defaultExcludes, errDuplicateIgnoreFile
	}

	if earthExists == earthlyExists {
		if !dockerExists {
			// return just the default excludes if neither of them exist
			return defaultExcludes, nil
		}

		ignoreFile = dockerIgnoreFile
	} else if earthlyExists {
		ignoreFile = earthlyIgnoreFile
	}

	filePath := filepath.Join(dir, ignoreFile)

	f, err := os.Open(filePath) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filePath, err)
	}
	defer f.Close()

	excludes, err := ignorefile.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}

	return append(excludes, defaultExcludes...), nil
}
