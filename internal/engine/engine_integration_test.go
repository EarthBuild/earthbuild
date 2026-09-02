//go:build integration

package engine_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/engine"
	. "github.com/stretchr/testify/assert"
)

func newDocker(ctx context.Context, cfg *engine.Config) (*engine.Client, error) {
	return engine.New(ctx, engine.DockerShell, cfg)
}

func newPodman(ctx context.Context, cfg *engine.Config) (*engine.Client, error) {
	return engine.New(ctx, engine.PodmanShell, cfg)
}

func TestEngineNew(t *testing.T) {
	t.Parallel()

	//nolint:goconst
	testCases := []struct {
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		binary  string
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)
			NotNil(t, eng)
		})
	}
}

func TestEngineScheme(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		binary  string
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		scheme  engine.Scheme
	}{
		{"docker", newDocker, engine.SchemeDocker},
		{"podman", newPodman, engine.SchemePodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			scheme := eng.Metadata().Scheme
			Equal(t, tC.scheme, scheme)
		})
	}
}

func TestEngineIsAvailable(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		binary  string
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			available := eng.IsAvailable(ctx)
			True(t, available)
		})
	}
}

func TestEngineVersion(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		binary  string
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			info, err := eng.Version(ctx)
			NoError(t, err)
			NotEmpty(t, info.ClientVersion)
		})
	}
}

func TestEngineContainerInfo(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		binary  string
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			testContainers := make([]string, 0, 3)
			testContainers = append(testContainers, "test-1", "test-2")

			cleanup, err := spawnTestContainers(ctx, tC.binary, testContainers...)
			t.Cleanup(cleanup)
			NoError(t, err)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			getInfos := append(testContainers, "missing") //nolint:gocritic
			info, err := eng.InspectContainers(ctx, getInfos...)
			NoError(t, err)
			NotNil(t, info)

			Len(t, info, 3)

			Equal(t, getInfos[0], info[0].Name)
			Equal(t, "docker.io/library/nginx:1.21", info[0].Image)

			Equal(t, getInfos[1], info[1].Name)
			Equal(t, "docker.io/library/nginx:1.21", info[1].Image)

			Equal(t, getInfos[2], info[2].Name)
			Equal(t, engine.StatusMissing, info[2].Status)
		})
	}
}

func TestEngineContainerRemove(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		binary  string
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			testContainers := []string{"remove-1", "remove-2"}
			cleanup, err := spawnTestContainers(ctx, tC.binary, testContainers...)
			t.Cleanup(cleanup)
			NoError(t, err)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			info, err := eng.InspectContainers(ctx, testContainers...)
			NoError(t, err)
			Len(t, info, 2)

			err = eng.RemoveContainer(ctx, true, testContainers...)
			NoError(t, err)

			info, err = eng.InspectContainers(ctx, testContainers...)
			NoError(t, err)
			Equal(t, engine.StatusMissing, info[0].Status)
			Equal(t, engine.StatusMissing, info[1].Status)
		})
	}
}

func TestEngineContainerStop(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		binary  string
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			testContainers := []string{"stop-1", "stop-2"}
			cleanup, err := spawnTestContainers(ctx, tC.binary, testContainers...)
			t.Cleanup(cleanup)
			NoError(t, err)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			info, err := eng.InspectContainers(ctx, testContainers...)
			NoError(t, err)
			Len(t, info, 2)

			err = eng.StopContainer(ctx, 0, testContainers...)
			NoError(t, err)

			_, err = eng.InspectContainers(ctx, testContainers...)
			NoError(t, err)
			Len(t, info, 2)
		})
	}
}

func TestEngineLogs(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		binary  string
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			testContainers := []string{"logs-1", "logs-2"}
			cleanup, err := spawnTestContainers(ctx, tC.binary, testContainers...)
			t.Cleanup(cleanup)
			NoError(t, err)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			logs, err := eng.ContainersLogs(ctx, testContainers...)
			NoError(t, err)
			Len(t, logs, 2)

			Equal(t, "output stream\n", logs[0].Stdout)
			Equal(t, "error stream\n", logs[0].Stderr)

			Equal(t, "output stream\n", logs[1].Stdout)
			Equal(t, "error stream\n", logs[1].Stderr)
		})
	}
}

func TestEngineContainerRun(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		binary  string
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			testContainers := []string{"create-1", "create-2"}

			specs := make([]engine.ContainerSpec, 0, len(testContainers))
			for _, name := range testContainers {
				specs = append(specs, engine.ContainerSpec{
					NameOrID:       name,
					ImageRef:       "docker.io/nginx:1.21",
					Privileged:     false,
					Envs:           map[string]string{"test": name},
					Labels:         map[string]string{"test": name},
					ContainerArgs:  []string{"nginx-debug", "-g", "daemon off;"},
					AdditionalArgs: []string{"--rm"},
					Mounts: []engine.Mount{
						{
							Type:     engine.MountVolume,
							Source:   "vol-" + name,
							Dest:     "/test",
							ReadOnly: true,
						},
					},
					Ports: []engine.Port{
						{
							IP:            "127.0.0.1",
							HostPort:      0,
							ContainerPort: 5678,
							Protocol:      engine.ProtocolTCP,
						},
					},
				})
			}

			defer func() {
				for _, name := range testContainers {
					// Roll our own cleanup since we can't use the spawn test containers helper... since
					// the whole point of this test is to create them with an engine. Also theres a volume
					cmd := exec.CommandContext(ctx, tC.binary, "rm", "-f", name) // #nosec G204
					_ = cmd.Run()                                                // Just best effort

					cmd = exec.CommandContext(ctx, tC.binary, "volume", "rm", "-f", "vol-"+name) // #nosec G204
					_ = cmd.Run()
				}
			}()

			info, err := eng.InspectContainers(ctx, testContainers...)
			NoError(t, err)
			Equal(t, engine.StatusMissing, info[0].Status)
			Equal(t, engine.StatusMissing, info[1].Status)

			err = eng.RunContainer(ctx, specs...)
			NoError(t, err)

			info, err = eng.InspectContainers(ctx, testContainers...)
			NoError(t, err)
			Equal(t, engine.StatusRunning, info[0].Status)
			Equal(t, engine.StatusRunning, info[1].Status)
		})
	}
}

func TestEngineImagePull(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		binary  string
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		refList []string
	}{
		{"docker", newDocker, []string{"nginx:1.21", "alpine:3.18"}},
		// Podman prefers... and exports fully-qualified image tags
		{"podman", newPodman, []string{"docker.io/nginx:1.21", "docker.io/alpine:3.18"}},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			// podman pull needs some potentially valid address to check against, otherwise panic
			eng, err := tC.newFunc(ctx, &engine.Config{
				LocalRegistryHostFileValue: "tcp://some-host:5309",
				Log:                        testLogger(),
			})
			NoError(t, err)

			err = eng.PullImage(ctx, tC.refList...)
			NoError(t, err)

			defer func() {
				for _, ref := range tC.refList {
					cmd := exec.CommandContext(ctx, "docker", "image", "rm", "-f", ref) // #nosec G204
					_ = cmd.Run()
				}
			}()
		})
	}
}

func TestEngineImageInfo(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		binary  string
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		refList []string
	}{
		{"docker", newDocker, []string{"info:1", "info:2"}},
		{"podman", newPodman, []string{"localhost/info:1", "localhost/info:2"}},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			cleanup, err := spawnTestImages(ctx, tC.binary, tC.refList...)
			NoError(t, err)
			t.Cleanup(cleanup)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			info, err := eng.InspectImages(ctx, tC.refList...)
			NoError(t, err)

			Len(t, info, 2)

			Contains(t, info[0].Tags, tC.refList[0])
			Contains(t, info[1].Tags, tC.refList[1])
		})
	}
}

func TestEngineImageRemove(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		binary  string
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
	}{
		{binary: "docker", newFunc: newDocker},
		{binary: "podman", newFunc: newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			refList := []string{"remove:1", "remove:2"}
			cleanup, err := spawnTestImages(ctx, tC.binary, refList...)
			NoError(t, err)
			t.Cleanup(cleanup)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			info, err := eng.InspectImages(ctx, refList...)
			NoError(t, err)
			Len(t, info, 2)

			err = eng.RemoveImage(ctx, true, refList...)
			NoError(t, err)

			info, err = eng.InspectImages(ctx, refList...)
			NoError(t, err)
			Empty(t, info)
		})
	}
}

func TestEngineImageTag(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		binary  string
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		tagList []string
	}{
		{"docker", newDocker, []string{"tag:1", "tag:2"}},
		{"podman", newPodman, []string{"localhost/tag:1", "localhost/tag:2"}},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			ref := "tag:me"
			cleanup, err := spawnTestImages(ctx, tC.binary, ref)
			NoError(t, err)
			t.Cleanup(cleanup)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			info, err := eng.InspectImage(ctx, ref)
			NoError(t, err)

			imageID := info.ID

			tags := make([]engine.Tag, 0, len(tC.tagList))
			for _, tagName := range tC.tagList {
				tags = append(tags, engine.Tag{
					SourceRef: imageID,
					TargetRef: tagName,
				})
			}

			err = eng.TagImage(ctx, tags...)
			NoError(t, err)

			infos, err := eng.InspectImages(ctx, tC.tagList...)
			NoError(t, err)

			Contains(t, infos[0].Tags, tC.tagList[0])
			Contains(t, infos[1].Tags, tC.tagList[1])
		})
	}
}

func TestEngineImageLoad(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		binary  string
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		ref     string
	}{
		{"docker", newDocker, "load:me"},
		{"podman", newPodman, "localhost/load:me"},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			cleanup, err := spawnTestImages(ctx, tC.binary, tC.ref)
			NoError(t, err)

			imgBuffer := &bytes.Buffer{}
			cmd := exec.CommandContext(ctx, tC.binary, "image", "save", tC.ref) // #nosec G204
			cmd.Stdout = bufio.NewWriter(imgBuffer)
			err = cmd.Run()
			NoError(t, err)

			cleanup()

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			err = eng.LoadImage(ctx, bufio.NewReader(imgBuffer))
			NoError(t, err)

			defer func() {
				cmd := exec.CommandContext(ctx, tC.binary, "image", "rm", "-f", tC.ref) // #nosec G204
				_ = cmd.Run()
			}()

			info, err := eng.InspectImage(ctx, tC.ref)
			NoError(t, err)
			Contains(t, info.Tags, tC.ref)
		})
	}
}

func TestEngineImageLoadHybrid(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		binary  string
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
		ref     string
	}{
		{"docker", newDocker, "hybrid:test"},
		{"podman", newPodman, "localhost/hybrid:test"},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			data, err := os.ReadFile("./testdata/hybrid.tar")
			NoError(t, err)

			reader := bytes.NewReader(data)

			err = eng.LoadImage(ctx, reader)
			NoError(t, err)

			defer func() {
				cmd := exec.CommandContext(ctx, tC.binary, "image", "rm", "-f", tC.ref) // #nosec G204
				_ = cmd.Run()
			}()

			info, err := eng.InspectImage(ctx, tC.ref)
			NoError(t, err)
			Contains(t, info.Tags, tC.ref)
		})
	}
}

func TestEngineVolumeInfo(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		binary  string
		newFunc func(context.Context, *engine.Config) (*engine.Client, error)
	}{
		{"docker", newDocker},
		{"podman", newPodman},
	}
	for _, tC := range testCases {
		t.Run(tC.binary, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			onlyIfBinaryIsInstalled(ctx, t, tC.binary)

			volList := []string{"test1", "test2"}
			cleanup, err := spawnTestVolumes(ctx, tC.binary, volList...)
			NoError(t, err)
			t.Cleanup(cleanup)

			eng, err := tC.newFunc(ctx, &engine.Config{Log: testLogger()})
			NoError(t, err)

			info, err := eng.InspectVolumes(ctx, volList...)
			NoError(t, err)
			Len(t, info, 2)
		})
	}
}

func onlyIfBinaryIsInstalled(ctx context.Context, t *testing.T, binary string) {
	t.Helper()

	if !isBinaryInstalled(ctx, binary) {
		t.Skipf("%s is not available for tests, skipping", binary)
	}
}

func isBinaryInstalled(ctx context.Context, binary string) bool {
	cmd := exec.CommandContext(ctx, binary, "--help")
	return cmd.Run() == nil
}

func spawnTestContainers(ctx context.Context, binary string, names ...string) (func(), error) {
	_ = removeContainers(ctx, binary, names...) // best effort
	err := startTestContainers(ctx, binary, names...)

	cleanup := func() {
		_ = removeContainers(ctx, binary, names...) // best-effort
	}
	if err != nil {
		return cleanup, err
	}

	err = waitForContainers(ctx, binary, names...)

	return cleanup, err
}

func startTestContainers(ctx context.Context, binary string, names ...string) error {
	var err error

	m := sync.Mutex{}
	wg := sync.WaitGroup{}
	image := "docker.io/library/nginx:1.21"

	pullErr := pullImageIfNecessary(ctx, binary, image)
	if pullErr != nil {
		return fmt.Errorf("failed to pull image %s: %w", image, pullErr)
	}

	for _, name := range names {
		wg.Add(1)

		go func(name string) {
			defer wg.Done()

			cmd := exec.CommandContext(ctx, binary, "run", "-d", "--rm", "--name", name, image, // #nosec G204,G702
				"sh", "-c", `echo output stream&&>&2 echo error stream&&sleep 100`)
			output, createErr := cmd.CombinedOutput()

			m.Lock()
			defer m.Unlock()

			if createErr != nil {
				err = errors.Join(err, fmt.Errorf("%s: %w", string(output), createErr))
			}
		}(name)
	}

	wg.Wait()

	return err
}

func pullImageIfNecessary(ctx context.Context, binary string, image string) error {
	cmd := exec.CommandContext(ctx, binary, "inspect", "--type=image", image) // #nosec G204,G702

	_, inspectErr := cmd.CombinedOutput()
	if inspectErr == nil {
		return nil
	}

	cmd = exec.CommandContext(ctx, binary, "pull", image) // #nosec G204,G702

	_, pullErr := cmd.CombinedOutput()
	if pullErr != nil {
		return fmt.Errorf("failed to pull image %s: %w", image, pullErr)
	}

	return nil
}

func removeContainers(ctx context.Context, binary string, names ...string) error {
	var err error

	m := sync.Mutex{}

	wg := sync.WaitGroup{}
	for _, name := range names {
		wg.Add(1)

		go func(name string) {
			defer wg.Done()

			removeCmd := exec.CommandContext(ctx, binary, "rm", "-f", name) // #nosec G204
			_, removeErr := removeCmd.CombinedOutput()

			m.Lock()
			defer m.Unlock()

			if removeErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to remove container %s", name))
			}
		}(name)
	}

	wg.Wait()

	return err
}

func waitForContainers(ctx context.Context, binary string, names ...string) error {
	var err error

	m := sync.Mutex{}
	wg := sync.WaitGroup{}

	for _, name := range names {
		const maxAttempts = 100

		wg.Add(1)

		go func(name string) {
			defer wg.Done()

			attempts := 0
			for attempts < maxAttempts {
				attempts++
				cmd := exec.CommandContext(ctx, binary, "inspect", "-f", "{{.State.Running}}", name) // #nosec G204

				output, inspectErr := cmd.CombinedOutput()
				if inspectErr != nil {
					m.Lock()
					err = errors.Join(err, inspectErr)
					m.Unlock()
					return
				}

				if strings.Contains(string(output), "true") {
					return
				}

				time.Sleep(time.Millisecond * 200)
			}

			m.Lock()
			defer m.Unlock()

			err = errors.Join(err, fmt.Errorf("failed to wait for container %s to start", name))
		}(name)
	}

	wg.Wait()

	return err
}

func spawnTestImages(ctx context.Context, binary string, refs ...string) (func(), error) {
	var err error

	for _, ref := range refs {
		cmd := exec.CommandContext(ctx, binary, "image", "pull", "docker.io/nginx:1.21")

		output, createErr := cmd.CombinedOutput()
		if createErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", string(output), createErr))
			break
		}

		cmd = exec.CommandContext(ctx, binary, "image", "tag", "docker.io/nginx:1.21", ref) // #nosec G204

		output, tagErr := cmd.CombinedOutput()
		if tagErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", string(output), tagErr))
			break
		}
	}

	return func() {
		for _, ref := range refs {
			cmd := exec.CommandContext(ctx, binary, "image", "rm", "-f", ref) // #nosec G204
			_ = cmd.Run()
		}
	}, err
}

func spawnTestVolumes(ctx context.Context, binary string, names ...string) (func(), error) {
	var err error

	for _, name := range names {
		cmd := exec.CommandContext(ctx, binary, "volume", "create", name) // #nosec G204

		output, createErr := cmd.CombinedOutput()
		if createErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %s: %w", string(output), name, createErr))
		}
	}

	return func() {
		for _, name := range names {
			cmd := exec.CommandContext(ctx, binary, "volume", "rm", "-f", name) // #nosec G204
			_ = cmd.Run()
		}
	}, err
}

func testLogger() conslogging.ConsoleLogger {
	var logs strings.Builder

	logger := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)

	return logger.WithWriter(&logs)
}
