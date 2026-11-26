package buildkitd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/EarthBuild/earthbuild/cmd/earth/disable_alpn"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/engine"
	client "github.com/moby/buildkit/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		installationName string
		want             string
	}{
		{
			installationName: "earth",
			want:             "earth-buildkitd",
		},
		{
			installationName: "earthbuild",
			want:             "earthbuild-buildkitd",
		},
		{
			installationName: "custom",
			want:             "custom-buildkitd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.installationName, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ContainerName(tt.installationName))
		})
	}
}

func TestVolumeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		installationName string
		want             string
	}{
		{
			installationName: "earth",
			want:             "earth-cache",
		},
		{
			installationName: "earthbuild",
			want:             "earthbuild-cache",
		},
		{
			installationName: "custom",
			want:             "custom-cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.installationName, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, VolumeName(tt.installationName))
		})
	}
}

func TestUpdateContainerAddrs(t *testing.T) {
	t.Parallel()

	const localRegistry = "http://127.0.0.1:8371"

	tests := []struct {
		name              string
		initialAddr       string
		wantAddr          string
		scheme            engine.Scheme
		wantFromContainer bool
	}{
		{
			name:              "podman preserves custom local port",
			initialAddr:       "tcp://127.0.0.1:8772",
			wantAddr:          "tcp://127.0.0.1:8772",
			scheme:            engine.SchemePodman,
			wantFromContainer: false,
		},
		{
			name:              "docker preserves container scheme address",
			initialAddr:       "docker-container://test-container",
			wantAddr:          "docker-container://test-container",
			scheme:            engine.SchemeDocker,
			wantFromContainer: false,
		},
		{
			name:              "docker preserves custom local TCP port",
			initialAddr:       "tcp://127.0.0.1:8772",
			wantAddr:          "tcp://127.0.0.1:8772",
			scheme:            engine.SchemeDocker,
			wantFromContainer: false,
		},
		{
			// Apple Container publishes no host ports; NewTestClient's stub stands in for the container address.
			name:              "apple container resolves container address",
			initialAddr:       "apple-container://test-container",
			wantAddr:          "docker-container://test-container",
			scheme:            engine.SchemeApple,
			wantFromContainer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eng := engine.NewTestClient(engine.Metadata{Scheme: tt.scheme})
			settings := Settings{
				BuildkitAddr:      tt.initialAddr,
				LocalRegistryAddr: localRegistry,
			}

			updateContainerAddrs(t.Context(), eng, "test-container", &settings)

			assert.Equal(t, tt.wantAddr, settings.BuildkitAddr)
			assert.Equal(t, tt.wantFromContainer, settings.addrFromContainer)
			assert.Equal(t, localRegistry, settings.LocalRegistryAddr)
		})
	}
}

func TestSettingsIsLocal(t *testing.T) {
	t.Parallel()

	t.Run("configured private IP is remote", func(t *testing.T) {
		t.Parallel()

		settings := Settings{BuildkitAddr: "tcp://192.168.64.2:8372"}
		assert.False(t, settings.isLocal())
	})

	t.Run("bridge IP resolved from a managed container is local", func(t *testing.T) {
		t.Parallel()

		settings := Settings{BuildkitAddr: "tcp://192.168.64.2:8372", addrFromContainer: true}
		assert.True(t, settings.isLocal())
	})

	t.Run("updateContainerAddrs marks the address as container-derived", func(t *testing.T) {
		t.Parallel()

		eng := engine.NewTestClient(engine.Metadata{Name: "Apple Container", Scheme: engine.SchemeApple})
		settings := Settings{BuildkitAddr: "apple-container://test-container"}
		updateContainerAddrs(t.Context(), eng, "test-container", &settings)
		assert.True(t, settings.addrFromContainer)
	})
}

func TestStart_InvalidAddrs(t *testing.T) {
	ctx := t.Context()
	log := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
	eng := engine.NewTestClient(engine.Metadata{
		Name:   "Docker",
		Scheme: engine.SchemeDocker,
	})

	tests := []struct {
		name        string
		errContains string
		settings    Settings
	}{
		{
			name: "invalid buildkit port",
			settings: Settings{
				BuildkitAddr: "tcp://localhost",
				UseTCP:       true,
			},
			errContains: "invalid port in buildkit address",
		},
		{
			name: "invalid local registry port",
			settings: Settings{
				BuildkitAddr:      "tcp://127.0.0.1:8372",
				LocalRegistryAddr: "tcp://localhost",
				UseTCP:            true,
			},
			errContains: "invalid port in local registry address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("EARTH_WITH_DOCKER", "")
			t.Setenv("EARTHLY_WITH_DOCKER", "")

			err := Start(ctx, log, "test-image", "test-container", eng, tt.settings, false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestPrintBuildkitInfo(t *testing.T) {
	t.Parallel()

	log := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
	info := &client.Info{
		BuildkitVersion: client.BuildkitVersion{
			Package:  "github.com/EarthBuild/buildkit",
			Version:  "v0.13.0",
			Revision: "abcd1234",
		},
	}

	t.Run("nil worker info does not panic", func(t *testing.T) {
		t.Parallel()

		assert.NotPanics(t, func() {
			printBuildkitInfo(log, info, nil, "v0.13.0", true, true)
		})
	})

	t.Run("populated worker info does not panic", func(t *testing.T) {
		t.Parallel()

		worker := &client.WorkerInfo{
			ParallelismMax:     4,
			ParallelismCurrent: 1,
			ParallelismWaiting: 0,
		}

		assert.NotPanics(t, func() {
			printBuildkitInfo(log, info, worker, "v0.13.0", true, true)
		})
	})
}

func TestPrepareServerCertsDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	caCertPath := filepath.Join(tmpDir, "ca_cert.pem")
	caKeyPath := filepath.Join(tmpDir, "ca_key.pem")
	serverCertPath := filepath.Join(tmpDir, "buildkit_cert.pem")
	serverKeyPath := filepath.Join(tmpDir, "buildkit_key.pem")
	clientCertPath := filepath.Join(tmpDir, "earthly_cert.pem")
	clientKeyPath := filepath.Join(tmpDir, "earthly_key.pem")

	require.NoError(t, os.WriteFile(caCertPath, []byte("CA CERT DATA"), 0o600))              // #nosec G306
	require.NoError(t, os.WriteFile(caKeyPath, []byte("SECRET CA KEY DATA"), 0o600))         // #nosec G306
	require.NoError(t, os.WriteFile(serverCertPath, []byte("SERVER CERT DATA"), 0o600))      // #nosec G306
	require.NoError(t, os.WriteFile(serverKeyPath, []byte("SERVER KEY DATA"), 0o600))        // #nosec G306
	require.NoError(t, os.WriteFile(clientCertPath, []byte("CLIENT CERT DATA"), 0o600))      // #nosec G306
	require.NoError(t, os.WriteFile(clientKeyPath, []byte("SECRET CLIENT KEY DATA"), 0o600)) // #nosec G306

	settings := Settings{
		TLSCA:         caCertPath,
		ServerTLSCert: serverCertPath,
		ServerTLSKey:  serverKeyPath,
		ClientTLSCert: clientCertPath,
		ClientTLSKey:  clientKeyPath,
	}

	serverCertsDir, err := prepareServerCertsDir(settings)
	require.NoError(t, err)

	// Verify the directory itself
	assert.Equal(t, filepath.Join(tmpDir, "buildkitd"), serverCertsDir)
	info, err := os.Stat(serverCertsDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	// Verify only server files are copied into the staging directory
	entries, err := os.ReadDir(serverCertsDir)
	require.NoError(t, err)

	copiedNames := make([]string, 0, len(entries))
	for _, e := range entries {
		copiedNames = append(copiedNames, e.Name())
	}

	assert.ElementsMatch(t, []string{"ca_cert.pem", "buildkit_cert.pem", "buildkit_key.pem"}, copiedNames)

	// Critical: Ensure CA key and client keys are NOT in serverCertsDir
	assert.NoFileExists(t, filepath.Join(serverCertsDir, "ca_key.pem"))
	assert.NoFileExists(t, filepath.Join(serverCertsDir, "earthly_key.pem"))
	assert.NoFileExists(t, filepath.Join(serverCertsDir, "earthly_cert.pem"))

	// Verify content correctness
	caData, err := os.ReadFile(filepath.Join(serverCertsDir, "ca_cert.pem")) // #nosec G304
	require.NoError(t, err)
	assert.Equal(t, "CA CERT DATA", string(caData))

	serverKeyData, err := os.ReadFile(filepath.Join(serverCertsDir, "buildkit_key.pem")) // #nosec G304
	require.NoError(t, err)
	assert.Equal(t, "SERVER KEY DATA", string(serverKeyData))

	keyInfo, err := os.Stat(filepath.Join(serverCertsDir, "buildkit_key.pem"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), keyInfo.Mode().Perm())

	// Test rejection of extraneous files to prevent credential leaks without deleting user files
	strayFile := filepath.Join(serverCertsDir, "stray_leak.key")
	require.NoError(t, os.WriteFile(strayFile, []byte("LEAKED SECRET"), 0o600)) // #nosec G306
	assert.FileExists(t, strayFile)

	_, err = prepareServerCertsDir(settings)
	require.Error(t, err)
	require.ErrorContains(t, err, `unexpected entry "stray_leak.key"`)
	assert.FileExists(t, strayFile)
}

func TestInstanceSettings(t *testing.T) {
	tmpHome := t.TempDir()
	instDir := filepath.Join(tmpHome, ".testinst", "certs")
	require.NoError(t, os.MkdirAll(instDir, 0o700))

	caFile := filepath.Join(instDir, "ca_cert.pem")
	certFile := filepath.Join(instDir, "earthly_cert.pem")
	keyFile := filepath.Join(instDir, "earthly_key.pem")

	require.NoError(t, os.WriteFile(caFile, []byte("CA"), 0o600))
	require.NoError(t, os.WriteFile(certFile, []byte("CERT"), 0o600))
	require.NoError(t, os.WriteFile(keyFile, []byte("KEY"), 0o600))

	t.Setenv("HOME", tmpHome)

	base := Settings{
		TLSCA:         "/default/ca.pem",
		ClientTLSCert: "/default/cert.pem",
		ClientTLSKey:  "/default/key.pem",
	}

	res := instanceSettings("testinst-buildkitd", base)
	assert.Equal(t, caFile, res.TLSCA)
	assert.Equal(t, certFile, res.ClientTLSCert)
	assert.Equal(t, keyFile, res.ClientTLSKey)

	// Non-matching container name falls back to base settings
	resFallback := instanceSettings("unknown-container", base)
	assert.Equal(t, base.TLSCA, resFallback.TLSCA)

	// Empty HOME falls back to base settings without creating relative path lookups
	t.Setenv("HOME", "")

	resEmptyHome := instanceSettings("testinst-buildkitd", base)
	assert.Equal(t, base.TLSCA, resEmptyHome.TLSCA)
}

func TestWaitUntilStopped(t *testing.T) {
	t.Parallel()

	t.Run("stopped or missing container succeeds", func(t *testing.T) {
		t.Parallel()

		eng := engine.NewTestClient(engine.Metadata{})

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		err := WaitUntilStopped(ctx, "test-container", eng)
		require.NoError(t, err)
	})

	t.Run("inspection error is propagated", func(t *testing.T) {
		t.Parallel()

		eng, err := engine.NewStub(&engine.Config{})
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		err = WaitUntilStopped(ctx, "test-container", eng)
		require.Error(t, err)
		require.ErrorContains(t, err, "inspect container test-container while waiting to stop")
	})

	t.Run("context cancellation returns error", func(t *testing.T) {
		t.Parallel()

		eng, err := engine.NewStub(&engine.Config{})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err = WaitUntilStopped(ctx, "test-container", eng)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestGetGCPolicySize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		policy   []client.PruneInfo
		wantSize int64
		wantOK   bool
	}{
		{name: "no catch-all policy", policy: []client.PruneInfo{{Filter: []string{"type==source.local"}, ReservedSpace: 5}}},
		{name: "legacy keepBytes lands in ReservedSpace", policy: []client.PruneInfo{{All: true, ReservedSpace: 7}}, wantSize: 7, wantOK: true},
		{name: "MaxUsedSpace is the cap", policy: []client.PruneInfo{{All: true, ReservedSpace: 7, MaxUsedSpace: 9}}, wantSize: 9, wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			size, ok := getGCPolicySize(&client.WorkerInfo{GCPolicy: tt.policy})
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantSize, size)
		})
	}
}
