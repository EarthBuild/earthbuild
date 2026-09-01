package buildkitd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/engine"
	client "github.com/moby/buildkit/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	appleContainerName = "Apple Container"
	dockerEngineName   = "Docker"
	defaultBuildkitTCP = "tcp://127.0.0.1:8372"
)

func TestEngineContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		engineName   string
		engineBinary string
		wantName     string
		wantDesc     string
	}{
		{
			name:       "docker",
			engineName: dockerEngineName,
			wantName:   dockerEngineName,
			wantDesc:   "Docker container",
		},
		{
			name:       "podman",
			engineName: "Podman",
			wantName:   "Podman",
			wantDesc:   "Podman container",
		},
		{
			name:       "apple container",
			engineName: appleContainerName,
			wantName:   appleContainerName,
			wantDesc:   appleContainerName,
		},
		{
			name:         "fallback to binary",
			engineBinary: "nerdctl",
			wantName:     "nerdctl",
			wantDesc:     "nerdctl container",
		},
		{
			name:         "fallback binary starting with vowel",
			engineBinary: "oci-runtime",
			wantName:     "oci-runtime",
			wantDesc:     "oci-runtime container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eng := engine.NewTestClient(engine.Metadata{
				Name:   tt.engineName,
				Binary: tt.engineBinary,
			})

			assert.Equal(t, tt.wantName, engineName(eng))
			assert.Equal(t, tt.wantDesc, engineContainer(eng))
		})
	}
}

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

	ctx := t.Context()

	t.Run("nil engine does nothing", func(t *testing.T) {
		t.Parallel()

		settings := Settings{
			BuildkitAddr:      defaultBuildkitTCP,
			LocalRegistryAddr: "http://127.0.0.1:8371",
		}
		updateContainerAddrs(ctx, nil, "test-container", &settings)
		assert.Equal(t, defaultBuildkitTCP, settings.BuildkitAddr)
		assert.Equal(t, "http://127.0.0.1:8371", settings.LocalRegistryAddr)
	})

	t.Run("docker engine uses container name", func(t *testing.T) {
		t.Parallel()

		eng := engine.NewTestClient(engine.Metadata{
			Name:   dockerEngineName,
			Scheme: engine.SchemeDocker,
		})
		settings := Settings{
			BuildkitAddr:      "docker-container://test-container",
			LocalRegistryAddr: "http://127.0.0.1:8371",
		}
		updateContainerAddrs(ctx, eng, "test-container", &settings)
		assert.Equal(t, "docker-container://test-container", settings.BuildkitAddr)
		assert.Equal(t, "http://127.0.0.1:8371", settings.LocalRegistryAddr)
	})
}

func TestStart_InvalidAddrs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	log := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
	eng := engine.NewTestClient(engine.Metadata{
		Name:   dockerEngineName,
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
				BuildkitAddr:      defaultBuildkitTCP,
				LocalRegistryAddr: "tcp://localhost",
				UseTCP:            true,
			},
			errContains: "invalid port in local registry address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := Start(ctx, log, "test-image", "test-container", eng, tt.settings, false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestStart_ContainerLocalRegistryAddr(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	log := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
	eng := engine.NewTestClient(engine.Metadata{
		Name:   dockerEngineName,
		Scheme: engine.SchemeDocker,
	})

	settings := Settings{
		BuildkitAddr:      defaultBuildkitTCP,
		LocalRegistryAddr: "docker-container://my-reg",
		UseTCP:            true,
	}

	// Should not fail with port parsing error for docker-container:// scheme.
	err := Start(ctx, log, "test-image", "test-container", eng, settings, false)
	if err != nil {
		assert.NotContains(t, err.Error(), "invalid port in local registry address")
	}
}

func TestStart_AppleContainerAddr(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	log := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
	eng := engine.NewTestClient(engine.Metadata{
		Name:   appleContainerName,
		Scheme: engine.SchemeApple,
	})

	settings := Settings{
		BuildkitAddr: "apple-container://earth-buildkitd",
		UseTCP:       true,
	}

	// Should not fail with port parsing error when UseTCP is true with apple-container scheme without explicit port.
	err := Start(ctx, log, "test-image", "test-container", eng, settings, false)
	if err != nil {
		assert.NotContains(t, err.Error(), "invalid port in buildkit address")
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

	// Test cleanup of extraneous files on subsequent run
	strayFile := filepath.Join(serverCertsDir, "stray_leak.key")
	require.NoError(t, os.WriteFile(strayFile, []byte("LEAKED SECRET"), 0o600)) // #nosec G306
	assert.FileExists(t, strayFile)

	_, err = prepareServerCertsDir(settings)
	require.NoError(t, err)
	assert.NoFileExists(t, strayFile)
}
