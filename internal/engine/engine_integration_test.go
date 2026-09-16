//go:build integration

package engine_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type engineTestTarget struct {
	binary    string
	tagPrefix string
	newFunc   func(context.Context, *engine.Config) (*engine.Client, error)
}

var availableEngines []engineTestTarget

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var err error
	availableEngines, err = discoverEngines(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(availableEngines) == 0 {
		fmt.Fprintln(os.Stderr, "warning: no container engines available for integration tests")
	}

	os.Exit(m.Run())
}

func discoverEngines(ctx context.Context) ([]engineTestTarget, error) {
	var targets []engineTestTarget

	// 1. Docker
	if _, err := exec.LookPath("docker"); err == nil {
		if err := ensureDockerStarted(ctx); err != nil {
			return nil, fmt.Errorf("ensure docker started: %w", err)
		}

		targets = append(targets, engineTestTarget{
			binary:    "docker",
			tagPrefix: "",
			newFunc:   newDocker,
		})
	}

	// 2. Podman
	if _, err := exec.LookPath("podman"); err == nil {
		if err := ensurePodmanStarted(ctx); err != nil {
			return nil, fmt.Errorf("ensure podman started: %w", err)
		}

		targets = append(targets, engineTestTarget{
			binary:    "podman",
			tagPrefix: "localhost/",
			newFunc:   newPodman,
		})
	}

	// 3. Apple Container
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("container"); err == nil {
			if err := ensureAppleContainerStarted(ctx); err != nil {
				return nil, fmt.Errorf("ensure apple container started: %w", err)
			}

			targets = append(targets, engineTestTarget{
				binary:    "container",
				tagPrefix: "docker.io/library/",
				newFunc:   newApple,
			})
		}
	}

	return targets, nil
}

func ensureDockerStarted(ctx context.Context) error {
	eng, err := newDocker(ctx, &engine.Config{Log: testLogger()})
	if err == nil && eng.IsAvailable(ctx) {
		return nil
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker binary not found: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		cmd := exec.CommandContext(ctx, "open", "-g", "-a", "Docker")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("start docker desktop: %w", err)
		}
	case "linux":
		cmd := exec.CommandContext(ctx, "systemctl", "--user", "start", "docker")
		_ = cmd.Run()
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for docker to start: %w", ctx.Err())
		case <-ticker.C:
			if eng, err := newDocker(ctx, &engine.Config{Log: testLogger()}); err == nil && eng.IsAvailable(ctx) {
				return nil
			}
		}
	}
}

func ensurePodmanStarted(ctx context.Context) error {
	eng, err := newPodman(ctx, &engine.Config{Log: testLogger()})
	if err == nil && eng.IsAvailable(ctx) {
		return nil
	}

	if _, err := exec.LookPath("podman"); err != nil {
		return fmt.Errorf("podman binary not found: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(startCtx, "podman", "machine", "start")
		_ = cmd.Run()
	case "linux":
		cmd := exec.CommandContext(ctx, "systemctl", "--user", "start", "podman.socket")
		_ = cmd.Run()
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for podman to start: %w", ctx.Err())
		case <-ticker.C:
			if eng, err := newPodman(ctx, &engine.Config{Log: testLogger()}); err == nil && eng.IsAvailable(ctx) {
				return nil
			}
		}
	}
}

func ensureAppleContainerStarted(ctx context.Context) error {
	eng, err := newApple(ctx, &engine.Config{Log: testLogger()})
	if err == nil && eng.IsAvailable(ctx) {
		return nil
	}

	if _, err := exec.LookPath("container"); err != nil {
		return fmt.Errorf("container binary not found: %w", err)
	}

	cmd := exec.CommandContext(ctx, "container", "system", "start")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start apple container service: %s: %w", string(out), err)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for apple container: %w", ctx.Err())
		case <-ticker.C:
			if eng, err := newApple(ctx, &engine.Config{Log: testLogger()}); err == nil && eng.IsAvailable(ctx) {
				return nil
			}
		}
	}
}

func newDocker(ctx context.Context, cfg *engine.Config) (*engine.Client, error) {
	return engine.New(ctx, engine.Docker, cfg)
}

func newPodman(ctx context.Context, cfg *engine.Config) (*engine.Client, error) {
	return engine.New(ctx, engine.Podman, cfg)
}

func newApple(ctx context.Context, cfg *engine.Config) (*engine.Client, error) {
	return engine.New(ctx, engine.AppleContainer, cfg)
}

func TestEngineNew(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			eng, err := target.newFunc(t.Context(), &engine.Config{Log: testLogger()})
			require.NoError(t, err)
			assert.NotNil(t, eng)
		})
	}
}

func TestEngineScheme(t *testing.T) {
	t.Parallel()

	wantSchemes := map[string]engine.Scheme{
		"docker":    engine.SchemeDocker,
		"podman":    engine.SchemePodman,
		"container": engine.SchemeApple,
	}

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			eng, err := target.newFunc(t.Context(), &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			assert.Equal(t, wantSchemes[target.binary], eng.Metadata().Scheme)
		})
	}
}

func TestEngineIsAvailable(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			available := eng.IsAvailable(ctx)
			assert.True(t, available)
		})
	}
}

func TestEngineVersion(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			info, err := eng.Version(ctx)
			require.NoError(t, err)
			assert.NotEmpty(t, info.ClientVersion)
		})
	}
}

func TestEngineListContainers(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			containers, err := eng.ListContainers(ctx)
			require.NoError(t, err)
			assert.NotNil(t, containers)
		})
	}
}

func TestEngineInspectContainers(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			testContainers := []string{"test-1", "test-2"}
			cleanup, err := spawnTestContainers(ctx, eng, testContainers...)
			t.Cleanup(cleanup)
			require.NoError(t, err)

			single, err := eng.InspectContainer(ctx, testContainers[0])
			require.NoError(t, err)
			assert.Equal(t, testContainers[0], single.Name)
			assert.Equal(t, "docker.io/library/nginx:1.21", single.Image)

			missingSingle, err := eng.InspectContainer(ctx, "missing")
			require.NoError(t, err)
			assert.Equal(t, "missing", missingSingle.Name)
			assert.Equal(t, engine.StatusMissing, missingSingle.Status)

			if target.binary == "container" {
				info, err := eng.InspectContainers(ctx, testContainers...)
				require.NoError(t, err)
				assert.Len(t, info, 2)
				assert.Equal(t, testContainers[0], info[0].Name)
				assert.Equal(t, "docker.io/library/nginx:1.21", info[0].Image)
				assert.Equal(t, testContainers[1], info[1].Name)
				assert.Equal(t, "docker.io/library/nginx:1.21", info[1].Image)

				missingInfo, mErr := eng.InspectContainers(ctx, "missing")
				require.NoError(t, mErr)
				require.Len(t, missingInfo, 1)
				assert.Equal(t, "missing", missingInfo[0].Name)
				assert.Equal(t, engine.StatusMissing, missingInfo[0].Status)

				return
			}

			getInfos := append(testContainers, "missing") //nolint:gocritic
			info, err := eng.InspectContainers(ctx, getInfos...)
			require.NoError(t, err)
			assert.NotNil(t, info)

			assert.Len(t, info, 3)

			assert.Equal(t, getInfos[0], info[0].Name)
			assert.Equal(t, "docker.io/library/nginx:1.21", info[0].Image)

			assert.Equal(t, getInfos[1], info[1].Name)
			assert.Equal(t, "docker.io/library/nginx:1.21", info[1].Image)

			assert.Equal(t, getInfos[2], info[2].Name)
			assert.Equal(t, engine.StatusMissing, info[2].Status)
		})
	}
}

func TestEngineRemoveContainer(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			testContainers := []string{"remove-1", "remove-2"}
			cleanup, err := spawnTestContainers(ctx, eng, testContainers...)
			t.Cleanup(cleanup)
			require.NoError(t, err)

			info, err := eng.InspectContainers(ctx, testContainers...)
			require.NoError(t, err)
			assert.Len(t, info, 2)

			err = eng.RemoveContainer(ctx, true, testContainers...)
			require.NoError(t, err)

			info, err = eng.InspectContainers(ctx, testContainers...)
			require.NoError(t, err)
			assert.Equal(t, engine.StatusMissing, info[0].Status)
			assert.Equal(t, engine.StatusMissing, info[1].Status)
		})
	}
}

func TestEngineStopContainers(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			testContainers := []string{"stop-1", "stop-2"}
			cleanup, err := spawnTestContainers(ctx, eng, testContainers...)
			t.Cleanup(cleanup)
			require.NoError(t, err)

			info, err := eng.InspectContainers(ctx, testContainers...)
			require.NoError(t, err)
			assert.Len(t, info, 2)

			err = eng.StopContainer(ctx, 0, testContainers...)
			require.NoError(t, err)

			info, err = eng.InspectContainers(ctx, testContainers...)
			require.NoError(t, err)
			assert.Len(t, info, 2)
			assert.Equal(t, engine.StatusExited, info[0].Status)
			assert.Equal(t, engine.StatusExited, info[1].Status)
		})
	}
}

func TestEngineContainersLogs(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			testContainers := []string{"logs-1", "logs-2"}
			cleanup, err := spawnTestContainers(ctx, eng, testContainers...)
			t.Cleanup(cleanup)
			require.NoError(t, err)

			logs, err := eng.ContainersLogs(ctx, testContainers...)
			require.NoError(t, err)
			assert.Len(t, logs, 2)

			for _, log := range logs {
				combined := log.Stdout + log.Stderr

				assert.Contains(t, combined, "output stream\n")
				assert.Contains(t, combined, "error stream\n")
			}
		})
	}
}

func TestEngineRunContainer(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			testContainers := []string{"create-1", "create-2"}

			specs := make([]engine.ContainerSpec, 0, len(testContainers))
			for _, name := range testContainers {
				// Apple Container CLI requires an explicit, non-zero host port for -p/--publish
				// and does not support ephemeral port allocation (port 0).
				hostPort := 0
				if target.binary == "container" {
					hostPort = getFreePort(t)
				}

				specs = append(specs, engine.ContainerSpec{
					NameOrID:       name,
					ImageRef:       "docker.io/library/nginx:1.21",
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
					PortMappings: []engine.PortMapping{
						{
							HostIP:        "127.0.0.1",
							HostPort:      hostPort,
							ContainerPort: 5678,
						},
					},
				})
			}

			defer func() {
				_ = eng.RemoveContainer(ctx, true, testContainers...)

				volNames := make([]string, len(testContainers))
				for i, name := range testContainers {
					volNames[i] = "vol-" + name
				}

				_ = eng.RemoveVolumes(ctx, true, volNames...)
			}()

			info, err := eng.InspectContainers(ctx, testContainers...)
			require.NoError(t, err)
			assert.Equal(t, engine.StatusMissing, info[0].Status)
			assert.Equal(t, engine.StatusMissing, info[1].Status)

			err = eng.RunContainer(ctx, specs...)
			require.NoError(t, err)

			info, err = eng.InspectContainers(ctx, testContainers...)
			require.NoError(t, err)
			assert.Equal(t, engine.StatusRunning, info[0].Status)
			assert.Equal(t, engine.StatusRunning, info[1].Status)
		})
	}
}

func TestEnginePullImage(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			// Use unique images to avoid removing the shared base image (nginx:1.21) used by parallel tests.
			refList := []string{
				"quay.io/libpod/alpine:latest",
				"quay.io/libpod/busybox:latest",
			}

			// podman pull needs some potentially valid address to check against, otherwise panic
			eng, err := target.newFunc(ctx, &engine.Config{
				LocalRegistryHost: "tcp://some-host:5309",
				Log:               testLogger(),
			})
			require.NoError(t, err)

			err = eng.PullImage(ctx, refList...)
			require.NoError(t, err)

			t.Cleanup(func() {
				_ = eng.RemoveImage(ctx, true, refList...)
			})
		})
	}
}

func TestEngineInspectImages(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			refList := []string{
				target.tagPrefix + "info:1",
				target.tagPrefix + "info:2",
			}

			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			cleanup, err := spawnTestImages(ctx, eng, refList...)
			require.NoError(t, err)
			t.Cleanup(cleanup)

			single, err := eng.InspectImage(ctx, refList[0])
			require.NoError(t, err)
			assert.Contains(t, single.Tags, refList[0])

			info, err := eng.InspectImages(ctx, refList...)
			require.NoError(t, err)

			assert.Len(t, info, 2)

			assert.Contains(t, info[0].Tags, refList[0])
			assert.Contains(t, info[1].Tags, refList[1])
		})
	}
}

func TestEngineRemoveImage(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			refList := []string{
				target.tagPrefix + "remove:1",
				target.tagPrefix + "remove:2",
			}

			cleanup, err := spawnTestImages(ctx, eng, refList...)
			require.NoError(t, err)
			t.Cleanup(cleanup)

			info, err := eng.InspectImages(ctx, refList...)
			require.NoError(t, err)
			assert.Len(t, info, 2)

			err = eng.RemoveImage(ctx, true, refList...)
			require.NoError(t, err)

			info, err = eng.InspectImages(ctx, refList...)
			require.NoError(t, err)

			for _, img := range info {
				assert.Empty(t, img.ID)
			}
		})
	}
}

func TestEngineTagImage(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			ref := target.tagPrefix + "tag:me"
			cleanup, err := spawnTestImages(ctx, eng, ref)
			require.NoError(t, err)
			t.Cleanup(cleanup)

			info, err := eng.InspectImage(ctx, ref)
			require.NoError(t, err)

			imageRef := info.ID
			if target.binary == "container" {
				imageRef = ref
			}

			tagList := []string{
				target.tagPrefix + "tag:1",
				target.tagPrefix + "tag:2",
			}

			for _, tagName := range tagList {
				err = eng.TagImage(ctx, imageRef, tagName)
				require.NoError(t, err)
			}

			infos, err := eng.InspectImages(ctx, tagList...)
			require.NoError(t, err)

			assert.Contains(t, infos[0].Tags, tagList[0])
			assert.Contains(t, infos[1].Tags, tagList[1])
		})
	}
}

func TestEngineLoadImage(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			ref := target.tagPrefix + "load:me"

			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			cleanup, err := spawnTestImages(ctx, eng, ref)
			require.NoError(t, err)

			imgBuffer := &bytes.Buffer{}
			imgWriter := bufio.NewWriter(imgBuffer)
			cmd := exec.CommandContext(ctx, target.binary, "image", "save", ref) // #nosec G204
			cmd.Stdout = imgWriter
			err = cmd.Run()
			assert.NoError(t, err)
			err = imgWriter.Flush()
			assert.NoError(t, err)

			cleanup()

			err = eng.LoadImage(ctx, bufio.NewReader(imgBuffer))
			if runtime.GOOS == "darwin" && target.binary == "podman" && err != nil && strings.Contains(err.Error(), "unsupported transport docker-archive") {
				t.Skip("podman on macOS does not support docker-archive transport")
			}

			require.NoError(t, err)

			defer func() {
				_ = eng.RemoveImage(ctx, true, ref)
			}()

			info, err := eng.InspectImage(ctx, ref)
			require.NoError(t, err)
			assert.Contains(t, info.Tags, ref)
		})
	}
}

func TestEngineLoadImageHybrid(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			ref := target.tagPrefix + "hybrid:test"

			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			data, err := os.ReadFile("./testdata/hybrid.tar")
			require.NoError(t, err)

			reader := bytes.NewReader(data)

			err = eng.LoadImage(ctx, reader)
			if runtime.GOOS == "darwin" && target.binary == "podman" && err != nil && strings.Contains(err.Error(), "unsupported transport docker-archive") {
				t.Skip("podman on macOS does not support docker-archive transport")
			}

			require.NoError(t, err)

			defer func() {
				_ = eng.RemoveImage(ctx, true, ref)
			}()

			info, err := eng.InspectImage(ctx, ref)
			require.NoError(t, err)
			assert.Contains(t, info.Tags, ref)
		})
	}
}

func TestEngineInspectVolumes(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			volList := []string{"test1", "test2"}
			cleanup, err := spawnTestVolumes(ctx, eng, target.binary, volList...)
			require.NoError(t, err)
			t.Cleanup(cleanup)

			single, err := eng.InspectVolume(ctx, volList[0])
			require.NoError(t, err)
			assert.Equal(t, volList[0], single.Name)

			info, err := eng.InspectVolumes(ctx, volList...)
			require.NoError(t, err)
			assert.Len(t, info, 2)
			assert.Equal(t, volList[0], info[0].Name)
			assert.Equal(t, volList[1], info[1].Name)
		})
	}
}

func TestEngineRemoveVolumes(t *testing.T) {
	t.Parallel()

	for _, target := range availableEngines {
		t.Run(target.binary, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			eng, err := target.newFunc(ctx, &engine.Config{Log: testLogger()})
			require.NoError(t, err)

			volList := []string{"remove-vol-1", "remove-vol-2"}
			cleanup, err := spawnTestVolumes(ctx, eng, target.binary, volList...)
			require.NoError(t, err)
			t.Cleanup(cleanup)

			info, err := eng.InspectVolumes(ctx, volList...)
			require.NoError(t, err)
			assert.Len(t, info, 2)

			err = eng.RemoveVolumes(ctx, true, volList...)
			require.NoError(t, err)

			info, err = eng.InspectVolumes(ctx, volList...)
			require.NoError(t, err)
			assert.Len(t, info, 2)
			assert.Zero(t, info[0].SizeBytes)
			assert.Zero(t, info[1].SizeBytes)
		})
	}
}

func getFreePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}

func spawnTestContainers(ctx context.Context, eng *engine.Client, names ...string) (func(), error) {
	_ = eng.RemoveContainer(ctx, true, names...) // best effort
	err := startTestContainers(ctx, eng, names...)

	cleanup := func() {
		_ = eng.RemoveContainer(ctx, true, names...) // best effort
	}
	if err != nil {
		return cleanup, err
	}

	err = waitForContainers(ctx, eng, names...)

	return cleanup, err
}

func startTestContainers(ctx context.Context, eng *engine.Client, names ...string) error {
	image := "docker.io/library/nginx:1.21"

	pullErr := pullImageIfNecessary(ctx, eng, image)
	if pullErr != nil {
		return fmt.Errorf("failed to pull image %s: %w", image, pullErr)
	}

	specs := make([]engine.ContainerSpec, len(names))
	for i, name := range names {
		specs[i] = engine.ContainerSpec{
			NameOrID:      name,
			ImageRef:      image,
			ContainerArgs: []string{"sh", "-c", "echo output stream&&>&2 echo error stream&&sleep 100"},
		}
	}

	return eng.RunContainer(ctx, specs...)
}

func waitForContainers(ctx context.Context, eng *engine.Client, names ...string) error {
	const maxAttempts = 100

	for range maxAttempts {
		infos, err := eng.InspectContainers(ctx, names...)
		if err == nil {
			allRunning := true
			for _, info := range infos {
				if info.Status != engine.StatusRunning {
					allRunning = false
					break
				}
			}

			if allRunning {
				return nil
			}
		}

		time.Sleep(time.Millisecond * 200)
	}

	return fmt.Errorf("failed to wait for containers %v to start", names)
}

func spawnTestImages(ctx context.Context, eng *engine.Client, refs ...string) (func(), error) {
	var err error
	const baseImage = "docker.io/library/nginx:1.21"

	pullErr := pullImageIfNecessary(ctx, eng, baseImage)
	if pullErr != nil {
		return func() {}, fmt.Errorf("pull base image %s: %w", baseImage, pullErr)
	}

	for _, ref := range refs {
		tagErr := eng.TagImage(ctx, baseImage, ref)
		if tagErr != nil {
			err = errors.Join(err, fmt.Errorf("tag image %s -> %s: %w", baseImage, ref, tagErr))
			break
		}
	}

	return func() {
		_ = eng.RemoveImage(ctx, true, refs...)
	}, err
}

func pullImageIfNecessary(ctx context.Context, eng *engine.Client, image string) error {
	info, err := eng.InspectImage(ctx, image)
	if err == nil && info.ID != "" {
		return nil
	}

	return eng.PullImage(ctx, image)
}

func spawnTestVolumes(ctx context.Context, eng *engine.Client, binary string, names ...string) (func(), error) {
	var err error

	_ = eng.RemoveVolumes(ctx, true, names...)

	for _, name := range names {
		cmd := exec.CommandContext(ctx, binary, "volume", "create", name) // #nosec G204

		output, createErr := cmd.CombinedOutput()
		if createErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %s: %w", string(output), name, createErr))
		}
	}

	return func() {
		_ = eng.RemoveVolumes(ctx, true, names...)
	}, err
}

func testLogger() *conslogging.ConsoleLogger {
	var logs strings.Builder

	logger := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)

	return logger.WithWriter(&logs)
}
