package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	earthCmdMu sync.Mutex
	testBinary string
)

func TestMain(m *testing.M) {
	binary, cleanup, err := buildEarthBinary()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to build earth test binary: %v\n", err)

		os.Exit(1)
	}

	testBinary = binary
	code := m.Run()

	cleanup()
	os.Exit(code)
}

func TestBuiltinArgCannotBePassedOnCommandLine(t *testing.T) {
	t.Parallel()

	for _, versionLine := range []string{
		"VERSION 0.8",
		"VERSION --arg-scope-and-set 0.8",
	} {
		t.Run(versionLine, func(t *testing.T) {
			t.Parallel()

			projectDir := copyFixtureDir(t, "builtin-args")
			replaceVersionLine(t, filepath.Join(projectDir, "Earthfile"), versionLine)

			out, err := runEarth(t, projectDir,
				"--no-output",
				"--build-arg", "EARTHLY_VERSION=123",
				"+builtin-args-test",
			)

			require.Error(t, err)
			require.Contains(t, out, "cannot be passed on the command line")
		})
	}
}

func TestConfigCommand(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	configPath := filepath.Join(projectDir, "config.yml")
	expectedDir := filepath.Join(repoRoot(), "tests", "cli", "testdata", "config")

	cmdOut, cmdErr := runEarth(t, projectDir, "--config", configPath, "config", "global.cache_size_mb", "10")
	require.Error(t, cmdErr)
	require.Contains(t, cmdOut, "failed to read from "+configPath)

	require.NoError(t, os.WriteFile(configPath, nil, 0o600))

	configSteps := []struct {
		name     string
		expected string
		args     []string
	}{
		{
			name:     "integer",
			args:     []string{"--config", configPath, "config", "global.cache_size_mb", "10"},
			expected: "expected-1.yml",
		},
		{
			name:     "nested string",
			args:     []string{"--config", configPath, "config", `git."example.com".password`, "hunter2"},
			expected: "expected-2.yml",
		},
		{
			name:     "list",
			args:     []string{"--config", configPath, "config", "global.buildkit_additional_args", "['userns', '--host']"},
			expected: "expected-3.yml",
		},
		{
			name:     "another integer",
			args:     []string{"--config", configPath, "config", "global.conversion_parallelism", "5"},
			expected: "expected-4.yml",
		},
		{
			name:     "delete",
			args:     []string{"--config", configPath, "config", "global.conversion_parallelism", "--delete"},
			expected: "expected-5.yml",
		},
	}

	for _, step := range configSteps {
		t.Run(step.name, func(t *testing.T) {
			stepOut, err := runEarth(t, projectDir, step.args...)
			require.NoError(t, err, stepOut)
			requireFileEquals(t, configPath, filepath.Join(expectedDir, step.expected))
		})
	}

	for _, helpArg := range []string{"--help", "-h"} {
		t.Run("help "+helpArg, func(t *testing.T) {
			before := readFile(t, configPath)
			helpOut, err := runEarth(
				t,
				projectDir,
				"--config",
				configPath,
				"config",
				"global.conversion_parallelism",
				helpArg,
			)
			require.NoError(t, err, helpOut)
			require.Equal(t, before, readFile(t, configPath))
		})
	}

	for _, invalidValue := range []string{"oops", ""} {
		t.Run("invalid conversion_parallelism "+invalidValue, func(t *testing.T) {
			t.Parallel()

			invalidOut, err := runEarth(
				t,
				projectDir,
				"--config",
				configPath,
				"config",
				"global.conversion_parallelism",
				invalidValue,
			)
			require.Error(t, err)
			require.Contains(t, invalidOut, "upsert config")
		})
	}

	finalOut, finalErr := runEarth(t, projectDir, "--config", configPath, "config", "global.buildkit_image", "")
	require.NoError(t, finalErr, finalOut)
}

func TestConfigCommandDefaultAndEnvLocations(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := t.TempDir()
	expectedConfig := filepath.Join(repoRoot(), "tests", "cli", "testdata", "config", "expected-1.yml")

	out, err := runEarthWithEnv(t, projectDir, []string{"HOME=" + home}, "config", "global.cache_size_mb", "10")
	require.NoError(t, err, out)
	requireFileEquals(t, filepath.Join(home, ".earthly", "config.yml"), expectedConfig)

	otherConfig := filepath.Join(home, ".earthly", "other-config.yml")
	require.NoError(t, os.WriteFile(otherConfig, nil, 0o600))

	out, err = runEarthWithEnv(
		t,
		projectDir,
		[]string{"HOME=" + home, "EARTHLY_CONFIG=" + otherConfig},
		"config",
		"global.cache_size_mb",
		"10",
	)
	require.NoError(t, err, out)
	requireFileEquals(t, otherConfig, expectedConfig)

	namedHome := filepath.Join(home, ".earthly-test2", "config.yml")
	out, err = runEarthWithEnv(
		t,
		projectDir,
		[]string{"HOME=" + home, "EARTHLY_INSTALLATION_NAME=earthly-test2"},
		"config",
		"global.cache_size_mb",
		"10",
	)
	require.NoError(t, err, out)
	requireFileEquals(t, namedHome, expectedConfig)
}

func TestConfigReadFailures(t *testing.T) {
	t.Parallel()

	projectDir := copyFixtureDir(t, "config")

	out, err := runEarth(t, projectDir, "--config=this-does-not-exist.yml", "+hello")
	require.Error(t, err)
	require.Contains(t, out, "failed to read from this-does-not-exist.yml")
}

func buildEarthBinary() (string, func(), error) {
	if binary := os.Getenv("EARTHLY_TEST_BINARY"); binary != "" {
		return binary, func() {}, nil
	}

	dir, err := os.MkdirTemp("", "earth-cli-test-*")
	if err != nil {
		return "", nil, err
	}

	binary := filepath.Join(dir, "earth")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	//nolint:gosec // This test builds the repository's own CLI binary.
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/earthly")
	cmd.Dir = repoRoot()

	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("%w\n%s", err, out)
	}

	return binary, func() { _ = os.RemoveAll(dir) }, nil
}

func runEarth(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()

	return runEarthWithEnv(t, dir, nil, args...)
}

func runEarthWithEnv(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()

	earthCmdMu.Lock()
	defer earthCmdMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	//nolint:gosec // The test controls both the binary path and arguments.
	cmd := exec.CommandContext(ctx, testBinary, args...)
	cmd.Dir = dir

	cmd.Env = envWithOverrides(os.Environ(), append([]string{
		"EARTHLY_DISABLE_AUTO_UPDATE=true",
		"EARTHLY_DISABLE_FRONTEND_DETECTION=true",
		"OTEL_SDK_DISABLED=true",
	}, env...)...)

	out, err := cmd.CombinedOutput()

	return string(out), err
}

func copyFixtureDir(t *testing.T, fixture string) string {
	t.Helper()

	src := filepath.Join(repoRoot(), "tests", "cli", "testdata", fixture)
	dst := t.TempDir()

	require.NoError(t, os.CopyFS(dst, os.DirFS(src)))

	return dst
}

func replaceVersionLine(t *testing.T, path, versionLine string) {
	t.Helper()

	//nolint:gosec // Test fixture paths are generated by the test helper.
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	lines := bytes.SplitN(data, []byte("\n"), 2)
	require.Len(t, lines, 2)
	lines[0] = []byte(versionLine)

	//nolint:gosec // This writes a temporary test fixture.
	require.NoError(t, os.WriteFile(path, bytes.Join(lines, []byte("\n")), 0o600))
}

func requireFileEquals(t *testing.T, actualPath, expectedPath string) {
	t.Helper()

	require.Equal(t, readFile(t, expectedPath), readFile(t, actualPath))
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	//nolint:gosec // Test fixture paths are generated by test helpers.
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
}

func envWithOverrides(base []string, overrides ...string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))

	add := func(entry string) {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			return
		}

		if _, exists := values[name]; !exists {
			order = append(order, name)
		}

		values[name] = value
	}

	for _, entry := range base {
		add(entry)
	}

	for _, entry := range overrides {
		add(entry)
	}

	env := make([]string, 0, len(values))
	for _, name := range order {
		env = append(env, name+"="+values[name])
	}

	return env
}

func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to locate test source")
	}

	dir := filepath.Dir(file)
	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			panic("failed to locate repository root from " + file)
		}

		dir = parent
	}
}
