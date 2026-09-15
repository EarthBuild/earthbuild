package common

// Only functions that do NOT touch the app CLI should go here!

import (
	"debug/buildinfo"
	"fmt"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/EarthBuild/earthbuild/util/hint"
	"github.com/EarthBuild/earthbuild/variables"
	gsysinfo "github.com/elastic/go-sysinfo"
)

// Wrap formats strings by joining them with newlines and tabs.
func Wrap(s ...string) string {
	return strings.Join(s, "\n\t")
}

// CombineVariables merges dot env map with flag arguments to return a new variable scope.
func CombineVariables(dotEnvMap map[string]string, flagArgs, buildFlagArgs []string) (*variables.Scope, error) {
	dotEnvVars := variables.NewScope()
	for k, v := range dotEnvMap {
		dotEnvVars.Add(k, v)
	}

	buildArgs := slices.Concat(buildFlagArgs, flagArgs)

	overridingVars, err := variables.ParseCommandLineArgs(buildArgs)
	if err != nil {
		return nil, fmt.Errorf("parse build args: %w", err)
	}

	return variables.CombineScopes(overridingVars, dotEnvVars), nil
}

// ProcessSecrets processes local and remote secrets.
func ProcessSecrets(
	secrets, secretFiles []string, dotEnvMap map[string]string, secretsFilePath string,
) (map[string][]byte, error) {
	finalSecrets := make(map[string][]byte)
	for k, v := range dotEnvMap {
		finalSecrets[k] = []byte(v)
	}

	for _, secret := range secrets {
		parts := strings.SplitN(secret, "=", 2)
		key := parts[0]

		var data []byte
		if len(parts) == 2 {
			// secret value passed as argument
			data = []byte(parts[1])
		} else {
			// Not set. Use environment to fetch it.
			value, found := os.LookupEnv(secret)
			if !found {
				err := fmt.Errorf("failed to set secret %q via --secret flag without a value", secret)

				return nil, hint.Wrapf(err,
					"Try to set an env var by the name %q with the secret value or pass the value as part of the --secret flag",
					secret)
			}

			data = []byte(value)
		}

		if _, ok := finalSecrets[key]; ok {
			err := fmt.Errorf("failed to set secret %q via --secret flag", key)

			return nil, hint.Wrapf(err,
				"Check the secret %q has not already been set in the file %q or passed more than once to the command",
				key, secretsFilePath)
		}

		finalSecrets[key] = data
	}

	for _, secret := range secretFiles {
		parts := strings.SplitN(secret, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("unable to parse --secret-file argument: %q", secret)
		}

		k := parts[0]
		path := fileutil.ExpandPath(parts[1])

		data, err := os.ReadFile(path) // #nosec G304
		if err != nil {
			return nil, fmt.Errorf("failed to open %q: %w", path, err)
		}

		if _, ok := finalSecrets[k]; ok {
			err := fmt.Errorf("failed to set secret %q via --secret-file flag", k)

			return nil, hint.Wrapf(err,
				"Check the secret %q has not already been set in the file %q, or passed via --secret flag",
				k, secretsFilePath)
		}

		finalSecrets[k] = data
	}

	return finalSecrets, nil
}

// GetPlatform returns the current operating system and architecture platform.
func GetPlatform() string {
	h, err := gsysinfo.Host()
	if err != nil {
		return "unknown"
	}

	info := h.Info()

	return fmt.Sprintf("%s/%s; %s %s", runtime.GOOS, runtime.GOARCH, info.OS.Name, info.OS.Version)
}

// GetBinaryName returns the default executable binary name for earthbuild.
func GetBinaryName() string {
	if len(os.Args) == 0 {
		return "earthly"
	}

	// can't use os.Executable() here; because it will give us earth if executed via the earth symlink
	binPath := os.Args[0]
	baseName := path.Base(binPath)

	return baseName
}

// IfNilBoolDefault returns the boolean value if ptr is set, or the default value otherwise.
func IfNilBoolDefault(ptr *bool, defaultValue bool) bool {
	if ptr == nil {
		return defaultValue
	}

	return *ptr
}

// earthBuildModulePath is the Go module path of the current EarthBuild binary.
const earthBuildModulePath = "github.com/EarthBuild/earthbuild"

// legacyEarthlyModulePath is the Go module path of pre-fork upstream earthly
// binaries, which bootstrap still needs to recognise so it can replace them.
const legacyEarthlyModulePath = "github.com/earthly/earthly"

// IsEarthBinary reports whether the file at path is an earth executable, by
// reading the Go module metadata embedded in the binary (without executing it)
// and comparing its main module path against the EarthBuild module path and the
// legacy upstream earthly one.
//
// It fails closed: anything that is not a readable Go binary built from one of
// those modules - a missing file, a directory, a truncated or non-Go file, or a
// permission error - reports false. Callers use this as a guard before
// destructive operations, so a false negative is safe while a false positive is
// not.
func IsEarthBinary(path string) bool {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return false
	}

	switch info.Main.Path {
	case earthBuildModulePath, legacyEarthlyModulePath:
		return true
	default:
		return false
	}
}
