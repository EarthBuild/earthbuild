package engine

import (
	"cmp"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/config"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var noopArgs = parsedCLIVals{}

type results struct {
	buildkit      string
	localRegistry string
}

type parsedCLIVals struct {
	buildkit string
}

func TestBuildArgMatrix(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	//nolint:goconst
	tests := []struct {
		testName string
		args     parsedCLIVals
		expected results
		config   config.GlobalConfig
	}{
		{
			testName: "No Config, no CLI",
			config: config.GlobalConfig{
				BuildkitHost:      "",
				LocalRegistryHost: "",
			},
			args: noopArgs,
			expected: results{
				buildkit:      "docker-container://test",
				localRegistry: "",
			},
		},
		{
			testName: "Remote Local in config, no CLI",
			config: config.GlobalConfig{
				BuildkitHost:      "tcp://127.0.0.1:8372",
				LocalRegistryHost: "",
			},
			args: noopArgs,
			expected: results{
				buildkit:      "tcp://127.0.0.1:8372",
				localRegistry: "",
			},
		},
		{
			testName: "Remote remote in config, no CLI",
			config: config.GlobalConfig{
				BuildkitHost:      "tcp://my-cool-host:8372",
				LocalRegistryHost: "",
			},
			args: noopArgs,
			expected: results{
				buildkit:      "tcp://my-cool-host:8372",
				localRegistry: "",
			},
		},
		{
			testName: "Nonstandard local in config, no CLI",
			config: config.GlobalConfig{
				BuildkitHost:      "docker-container://my-container",
				LocalRegistryHost: "",
			},
			args: noopArgs,
			expected: results{
				buildkit:      "docker-container://my-container",
				localRegistry: "",
			},
		},
		{
			testName: "Remote Local in config, no CLI, validate registry host",
			config: config.GlobalConfig{
				BuildkitHost:      "tcp://127.0.0.1:8372",
				LocalRegistryHost: "tcp://127.0.0.1:8371",
			},
			args: noopArgs,
			expected: results{
				buildkit:      "tcp://127.0.0.1:8372",
				localRegistry: "tcp://127.0.0.1:8371",
			},
		},
		{
			testName: "Remote remote in config, no CLI, skip validate registry host",
			config: config.GlobalConfig{
				BuildkitHost:      "tcp://my-cool-host:8372",
				LocalRegistryHost: "this-is-not-a-url",
			},
			args: noopArgs,
			expected: results{
				buildkit:      "tcp://my-cool-host:8372",
				localRegistry: "",
			},
		},
		{
			testName: "Local in config, no CLI, validate registry host",
			config: config.GlobalConfig{
				BuildkitHost:      "docker-container://my-cool-container",
				LocalRegistryHost: "tcp://127.0.0.1:8371",
			},
			args: noopArgs,
			expected: results{
				buildkit:      "docker-container://my-cool-container",
				localRegistry: "tcp://127.0.0.1:8371",
			},
		},
	}

	for _, tt := range tests {
		var logs strings.Builder

		logger := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
		logger = logger.WithWriter(&logs)

		urls, err := ResolveAddrs(Docker, &Config{
			BuildkitHost:      cmp.Or(tt.args.buildkit, tt.config.BuildkitHost),
			LocalRegistryHost: tt.config.LocalRegistryHost,
			ContainerName:     "test", //nolint:goconst
			DefaultPort:       DefaultBuildkitPort,
			Log:               logger,
		})
		r.NoError(err)
		assert.Equal(t, tt.expected, results{
			buildkit:      urls.Buildkit.String(),
			localRegistry: urls.LocalRegistry.String(),
		})
	}
}

func TestBuildArgMatrixValidationFailures(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	//nolint:goconst
	tests := []struct {
		testName string
		log      string
		expected error
		config   config.GlobalConfig
	}{
		{
			testName: "Invalid buildkit URL",
			config: config.GlobalConfig{
				BuildkitHost:      "http\r://foo.com/",
				LocalRegistryHost: "",
			},
			expected: errInvalidURL,
			log:      "",
		},
		{
			testName: "Invalid registry URL",
			config: config.GlobalConfig{
				BuildkitHost:      "",
				LocalRegistryHost: "http\r://foo.com/",
			},
			expected: errInvalidURL,
			log:      "",
		},
		{
			testName: "Homebrew test",
			config: config.GlobalConfig{
				BuildkitHost:      "127.0.0.1",
				LocalRegistryHost: "",
			},
			expected: errInvalidScheme,
			log:      "",
		},
	}

	for _, tt := range tests {
		var logs strings.Builder

		logger := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
		logger = logger.WithWriter(&logs)

		_, err := ResolveAddrs(Docker, &Config{
			BuildkitHost:      tt.config.BuildkitHost,
			LocalRegistryHost: tt.config.LocalRegistryHost,
			Log:               logger,
			ContainerName:     "test",
			DefaultPort:       DefaultBuildkitPort,
		})
		r.ErrorIs(err, tt.expected)
		assert.Contains(t, logs.String(), tt.log)
	}
}

func TestParseURLFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expected error
		testName string
		url      string
	}{
		{
			testName: "Invalid URL",
			url:      "http\r://foo.com/",
			expected: errInvalidURL,
		},
		{
			testName: "Invalid Scheme",
			url:      "gopher://my-hole",
			expected: errInvalidScheme,
		},
		{
			testName: "Missing Port",
			url:      "tcp://my-server",
			expected: errMissingPort,
		},
	}

	for _, tt := range tests {
		_, err := parseAddr(tt.url)
		assert.ErrorIs(t, err, tt.expected)
	}
}

func TestParseAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		testName string
		url      string
	}{
		{
			"docker-container URL",
			"docker-container://my-container",
		},
		{
			"tcp URL",
			"tcp://my-host:42",
		},
	}

	for _, tt := range tests {
		_, err := parseAddr(tt.url)
		assert.NoError(t, err)
	}
}

func TestResolveAddrsDrivers(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		ContainerName: "test",
		DefaultPort:   DefaultBuildkitPort,
	}

	tests := []struct {
		driver       Driver
		wantBuildkit string
		wantErr      bool
	}{
		{driver: Docker, wantBuildkit: "docker-container://test"},
		{driver: DockerShell, wantBuildkit: "docker-container://test"},
		{driver: AppleContainer, wantBuildkit: "apple-container://test"},
		{driver: Podman, wantBuildkit: "tcp://127.0.0.1:8372"},
		{driver: PodmanShell, wantBuildkit: "tcp://127.0.0.1:8372"},
		{driver: Stub, wantBuildkit: "docker-container://test"},
		{driver: Auto, wantErr: true},
		{driver: Driver("unsupported"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(string(tt.driver), func(t *testing.T) {
			t.Parallel()

			urls, err := ResolveAddrs(tt.driver, cfg)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBuildkit, urls.Buildkit.String())
		})
	}
}

func TestResolveAddrsLogging(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	tests := []struct {
		testName string
		log      string
		config   config.GlobalConfig
	}{
		{
			testName: "Buildkit/Local Registry host mismatch, schemes match",
			config: config.GlobalConfig{
				BuildkitHost:      "tcp://localhost:8372",
				LocalRegistryHost: "tcp://remotehost:8371",
			},
			log: "Buildkit and local registry URLs are pointed at different hosts",
		},
	}

	for _, tt := range tests {
		var logs strings.Builder

		logger := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
		logger = logger.WithWriter(&logs)

		_, err := ResolveAddrs(Docker, &Config{
			BuildkitHost:      tt.config.BuildkitHost,
			LocalRegistryHost: tt.config.LocalRegistryHost,
			Log:               logger,
			ContainerName:     "test",
			DefaultPort:       DefaultBuildkitPort,
		})
		r.NoError(err)
		assert.Contains(t, logs.String(), tt.log)
	}
}

func TestResolveAddrsLoggingNonIssues(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	tests := []struct {
		testName string
		log      string
		config   config.GlobalConfig
	}{
		{
			testName: "Buildkit/Local Registry host mismatch, schemes differ",
			config: config.GlobalConfig{
				BuildkitHost:      "docker-container://127.0.0.1:8372",
				LocalRegistryHost: "tcp://localhost:8371",
			},
			log: "Buildkit and local registry URLs are pointed at different hosts",
		},
		{
			testName: "Buildkit/Debugger host mismatch, schemes differ",
			config: config.GlobalConfig{
				BuildkitHost:      "docker-container://bk:1234",
				LocalRegistryHost: "",
			},
			log: "Buildkit and Debugger URLs are pointed at different hosts",
		},
	}

	for _, tt := range tests {
		var logs strings.Builder

		logger := conslogging.Current(conslogging.DefaultPadding, conslogging.Info, false)
		logger = logger.WithWriter(&logs)

		_, err := ResolveAddrs(Docker, &Config{
			BuildkitHost:      tt.config.BuildkitHost,
			LocalRegistryHost: tt.config.LocalRegistryHost,
			Log:               logger,
			ContainerName:     "test",
			DefaultPort:       DefaultBuildkitPort,
		})
		r.NoError(err)
		assert.NotContains(t, logs.String(), tt.log)
	}
}

func TestDriverDefaultAddr(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		ContainerName: "custom-buildkitd",
		DefaultPort:   9999,
	}

	tests := []struct {
		driver   engineDriver
		expected string
	}{
		{
			driver:   &dockerEngine{},
			expected: "docker-container://custom-buildkitd",
		},
		{
			driver:   &podmanEngine{},
			expected: "tcp://127.0.0.1:9999",
		},
		{
			driver:   &appleEngine{},
			expected: "apple-container://custom-buildkitd",
		},
		{
			driver:   &stubEngine{},
			expected: "docker-container://custom-buildkitd",
		},
	}

	r := require.New(t)

	for _, tt := range tests {
		addr, err := tt.driver.DefaultAddr(cfg)
		r.NoError(err)
		assert.Equal(t, tt.expected, addr)
	}
}

func TestContainerAddr(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := require.New(t)

	t.Run("docker", func(t *testing.T) {
		t.Parallel()

		e := &dockerEngine{}
		addr, err := e.ContainerAddr(ctx, "my-container", DefaultBuildkitPort)
		r.NoError(err)
		assert.Equal(t, "docker-container://my-container", addr)

		addr, err = e.ContainerAddr(ctx, "my-container", DefaultLocalRegistryPort)
		r.NoError(err)
		assert.Equal(t, "tcp://127.0.0.1:8371", addr)
	})

	t.Run("podman", func(t *testing.T) {
		t.Parallel()

		e := &podmanEngine{}
		addr, err := e.ContainerAddr(ctx, "my-container", DefaultBuildkitPort)
		r.NoError(err)
		assert.Equal(t, "tcp://127.0.0.1:8372", addr)

		addr, err = e.ContainerAddr(ctx, "my-container", DefaultLocalRegistryPort)
		r.NoError(err)
		assert.Equal(t, "tcp://127.0.0.1:8371", addr)
	})

	t.Run("stub", func(t *testing.T) {
		t.Parallel()

		e := &stubEngine{}
		addr, err := e.ContainerAddr(ctx, "my-container", DefaultBuildkitPort)
		r.NoError(err)
		assert.Equal(t, "docker-container://my-container", addr)

		addr, err = e.ContainerAddr(ctx, "my-container", DefaultLocalRegistryPort)
		r.NoError(err)
		assert.Equal(t, "tcp://127.0.0.1:8371", addr)
	})
}

//nolint:goconst
func TestImageLoadCommand(t *testing.T) {
	t.Parallel()

	filenameWithSpaces := "/tmp/path with spaces/my image.tar"
	filenameWithInjection := "/tmp/image.tar; rm -rf /"

	t.Run("docker quotes filename", func(t *testing.T) {
		t.Parallel()

		e := &dockerEngine{shellEngine: &shellEngine{BinaryName: "docker"}}
		cmd := e.ImageLoadCommand(filenameWithSpaces)
		assert.Equal(t, "cat '/tmp/path with spaces/my image.tar' | docker load", cmd)

		cmdInj := e.ImageLoadCommand(filenameWithInjection)
		assert.Equal(t, "cat '/tmp/image.tar; rm -rf /' | docker load", cmdInj)
	})

	t.Run("apple container quotes filename", func(t *testing.T) {
		t.Parallel()

		e := &appleEngine{shellEngine: &shellEngine{BinaryName: "container"}}
		cmd := e.ImageLoadCommand(filenameWithSpaces)
		assert.Equal(t, "container image load --input '/tmp/path with spaces/my image.tar'", cmd)

		cmdInj := e.ImageLoadCommand(filenameWithInjection)
		assert.Equal(t, "container image load --input '/tmp/image.tar; rm -rf /'", cmdInj)
	})

	t.Run("podman quotes filename", func(t *testing.T) {
		t.Parallel()

		e := &podmanEngine{shellEngine: &shellEngine{BinaryName: "podman"}}
		cmd := e.ImageLoadCommand(filenameWithSpaces)
		assert.Equal(t, "podman pull 'docker-archive:/tmp/path with spaces/my image.tar'", cmd)

		cmdInj := e.ImageLoadCommand(filenameWithInjection)
		assert.Equal(t, "podman pull 'docker-archive:/tmp/image.tar; rm -rf /'", cmdInj)
	})

	t.Run("stub engine returns empty string", func(t *testing.T) {
		t.Parallel()

		e := &stubEngine{}
		assert.Empty(t, e.ImageLoadCommand(filenameWithSpaces))
	})
}

func TestUsesTCP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		scheme string
		want   bool
	}{
		{"tcp", true},
		{"apple-container", true},
		{"docker-container", false},
		{"podman-container", false},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.scheme, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, UsesTCP(tt.scheme))
		})
	}
}

func TestSchemeCapabilities(t *testing.T) {
	t.Parallel()

	assert.True(t, SchemeDocker.SupportsRegistryProxy())
	assert.False(t, SchemePodman.SupportsRegistryProxy())
	assert.False(t, SchemeApple.SupportsRegistryProxy())
	assert.False(t, SchemeTCP.SupportsRegistryProxy())
	assert.False(t, SchemeInvalid.SupportsRegistryProxy())

	assert.True(t, SchemePodman.RequiresTLSByDefault())
	assert.True(t, SchemeApple.RequiresTLSByDefault())
	assert.False(t, SchemeDocker.RequiresTLSByDefault())
	assert.False(t, SchemeTCP.RequiresTLSByDefault())
	assert.False(t, SchemeInvalid.RequiresTLSByDefault())
}

func TestPortMappingString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		want    string
		mapping PortMapping
	}{
		{
			name: "full mapping with host IP and port",
			want: "127.0.0.1:8372:8372",
			mapping: PortMapping{
				HostIP:        "127.0.0.1",
				HostPort:      DefaultBuildkitPort,
				ContainerPort: DefaultBuildkitPort,
			},
		},
		{
			name: "host IP with dynamic host port",
			mapping: PortMapping{
				HostIP:        "127.0.0.1",
				HostPort:      0,
				ContainerPort: 5678,
			},
			want: "127.0.0.1::5678",
		},
		{
			name: "host port and container port without host IP",
			mapping: PortMapping{
				HostPort:      8080,
				ContainerPort: 80,
			},
			want: "8080:80",
		},
		{
			name: "container port only",
			mapping: PortMapping{
				ContainerPort: 80,
			},
			want: "80",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.mapping.String())
		})
	}
}

func BenchmarkIsLocal(b *testing.B) {
	addrs := []string{
		"docker-container://earth-buildkitd",
		"podman-container://earth-buildkitd",
		"apple-container://earth-buildkitd",
		"tcp://127.0.0.1:8372",
		"tcp://localhost:8372",
		"tcp://[::1]:8372",
		"tcp://192.168.1.100:8372",
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, addr := range addrs {
			_ = IsLocal(addr)
		}
	}
}

func TestMatchesImageRef(t *testing.T) {
	t.Parallel()

	//nolint:goconst
	tests := []struct {
		name string
		tag  string
		ref  string
		want bool
	}{
		{
			name: "exact match identical",
			tag:  "alpine:3.18",
			ref:  "alpine:3.18",
			want: true,
		},
		{
			name: "exact match untagged",
			tag:  "alpine",
			ref:  "alpine",
			want: true,
		},
		{
			name: "exact match both latest",
			tag:  "alpine:latest",
			ref:  "alpine:latest",
			want: true,
		},
		{
			name: "tag with docker.io/library/ and ref untagged",
			tag:  "docker.io/library/alpine",
			ref:  "alpine",
			want: true,
		},
		{
			name: "ref with docker.io/library/ and tag untagged",
			tag:  "alpine",
			ref:  "docker.io/library/alpine",
			want: true,
		},
		{
			name: "tag with docker.io/ and ref untagged",
			tag:  "docker.io/myorg/myimg",
			ref:  "myorg/myimg",
			want: true,
		},
		{
			name: "tag latest and ref untagged",
			tag:  "alpine:latest",
			ref:  "alpine",
			want: true,
		},
		{
			name: "tag untagged and ref latest",
			tag:  "alpine",
			ref:  "alpine:latest",
			want: true,
		},
		{
			name: "tag docker.io/library/alpine:latest and ref alpine",
			tag:  "docker.io/library/alpine:latest",
			ref:  "alpine",
			want: true,
		},
		{
			name: "tag alpine and ref docker.io/library/alpine:latest",
			tag:  "alpine",
			ref:  "docker.io/library/alpine:latest",
			want: true,
		},
		{
			name: "registry with port untagged and ref latest",
			tag:  "localhost:5000/myrepo",
			ref:  "localhost:5000/myrepo:latest",
			want: true,
		},
		{
			name: "registry with port latest and ref untagged",
			tag:  "localhost:5000/myrepo:latest",
			ref:  "localhost:5000/myrepo",
			want: true,
		},
		{
			name: "registry with port exact match with version tag",
			tag:  "localhost:5000/myrepo:v1",
			ref:  "localhost:5000/myrepo:v1",
			want: true,
		},
		{
			name: "different tags",
			tag:  "alpine:v1",
			ref:  "alpine:v2",
			want: false,
		},
		{
			name: "tag v1 and ref untagged",
			tag:  "alpine:v1",
			ref:  "alpine",
			want: false,
		},
		{
			name: "tag untagged and ref v1",
			tag:  "alpine",
			ref:  "alpine:v1",
			want: false,
		},
		{
			name: "tag v1 and ref latest",
			tag:  "alpine:v1",
			ref:  "alpine:latest",
			want: false,
		},
		{
			name: "registry with port tag mismatch",
			tag:  "localhost:5000/myrepo:v1",
			ref:  "localhost:5000/myrepo:latest",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, matchesImageRef(tt.tag, tt.ref))
		})
	}
}

func TestAlignContainers(t *testing.T) {
	t.Parallel()

	c1 := Container{
		ID:     "c1-id-12345",
		Name:   "/c1",
		Status: "running",
	}
	c2 := Container{
		ID:     "c2-id-67890",
		Name:   "/c2",
		Status: "exited",
	}

	t.Run("unique containers", func(t *testing.T) {
		t.Parallel()

		res, err := alignContainers([]string{"c1", "c2"}, []Container{c2, c1})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, "running", res[0].Status)
		assert.Equal(t, "exited", res[1].Status)
	})

	t.Run("non-unique container names", func(t *testing.T) {
		t.Parallel()

		res, err := alignContainers([]string{"c1", "c1"}, []Container{c1})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, c1.ID, res[0].ID)
		assert.Equal(t, c1.ID, res[1].ID)
	})

	t.Run("non-unique name and ID prefix", func(t *testing.T) {
		t.Parallel()

		res, err := alignContainers([]string{"c1", "c1-id"}, []Container{c1})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, c1.ID, res[0].ID)
		assert.Equal(t, c1.ID, res[1].ID)
	})

	t.Run("duplicate with missing container", func(t *testing.T) {
		t.Parallel()

		res, err := alignContainers([]string{"c1", "missing", "c1"}, []Container{c1})
		require.NoError(t, err)
		require.Len(t, res, 3)
		assert.Equal(t, c1.ID, res[0].ID)
		assert.Equal(t, StatusMissing, res[1].Status)
		assert.Equal(t, "missing", res[1].Name)
		assert.Equal(t, c1.ID, res[2].ID)
	})

	t.Run("unmatched container in found returns error", func(t *testing.T) {
		t.Parallel()

		unmatched := Container{ID: "unmatched-id", Name: "unmatched"}
		res, err := alignContainers([]string{"c1"}, []Container{c1, unmatched})
		require.Error(t, err)
		assert.Nil(t, res)
	})
}

func TestAlignImages(t *testing.T) {
	t.Parallel()

	imgAlpine := Image{
		ID:   "sha256:alpine123",
		Tags: []string{"alpine:latest", "alpine:3.18"},
	}
	imgUbuntu := Image{
		ID:   "sha256:ubuntu456",
		Tags: []string{"ubuntu:latest"},
	}

	t.Run("unique images", func(t *testing.T) {
		t.Parallel()

		res, err := alignImages([]string{"ubuntu", "alpine"}, []Image{imgAlpine, imgUbuntu})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, imgUbuntu.ID, res[0].ID)
		assert.Equal(t, imgAlpine.ID, res[1].ID)
	})

	t.Run("non-unique exact references", func(t *testing.T) {
		t.Parallel()

		res, err := alignImages([]string{"alpine", "alpine"}, []Image{imgAlpine})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, imgAlpine.ID, res[0].ID)
		assert.Equal(t, imgAlpine.ID, res[1].ID)
	})

	t.Run("non-unique equivalent references", func(t *testing.T) {
		t.Parallel()

		res, err := alignImages([]string{"alpine", "alpine:latest"}, []Image{imgAlpine})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, imgAlpine.ID, res[0].ID)
		assert.Equal(t, imgAlpine.ID, res[1].ID)
	})

	t.Run("non-unique tag and ID prefix", func(t *testing.T) {
		t.Parallel()

		res, err := alignImages([]string{"alpine:latest", "sha256:alpine"}, []Image{imgAlpine})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, imgAlpine.ID, res[0].ID)
		assert.Equal(t, imgAlpine.ID, res[1].ID)
	})

	t.Run("ID matching with and without sha256 prefix", func(t *testing.T) {
		t.Parallel()

		imgCustom := Image{
			ID:   "sha256:abcdef123456",
			Tags: []string{"myimage:v1"},
		}

		res, err := alignImages([]string{"abcdef123", "sha256:abcdef"}, []Image{imgCustom})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, imgCustom.ID, res[0].ID)
		assert.Equal(t, imgCustom.ID, res[1].ID)
	})

	t.Run("unmatched image in found returns error", func(t *testing.T) {
		t.Parallel()

		unmatched := Image{ID: "sha256:rogue", Tags: []string{"rogue:latest"}}
		res, err := alignImages([]string{"alpine"}, []Image{imgAlpine, unmatched})
		require.Error(t, err)
		assert.Nil(t, res)
	})
}

//nolint:goconst
func TestAlignVolumes(t *testing.T) {
	t.Parallel()

	v1 := Volume{
		Name:       "vol1",
		Mountpoint: "/var/lib/docker/volumes/vol1/_data",
		SizeBytes:  1024,
	}
	v2 := Volume{
		Name:       "vol2",
		Mountpoint: "/var/lib/docker/volumes/vol2/_data",
		SizeBytes:  2048,
	}

	t.Run("unique volumes", func(t *testing.T) {
		t.Parallel()

		res, err := alignVolumes([]string{"vol2", "vol1"}, []Volume{v1, v2})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, v2.Name, res[0].Name)
		assert.Equal(t, v1.Name, res[1].Name)
	})

	t.Run("non-unique volume names", func(t *testing.T) {
		t.Parallel()

		res, err := alignVolumes([]string{"vol1", "vol1"}, []Volume{v1})
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, v1.Mountpoint, res[0].Mountpoint)
		assert.Equal(t, v1.Mountpoint, res[1].Mountpoint)
	})

	t.Run("duplicate with missing volume", func(t *testing.T) {
		t.Parallel()

		res, err := alignVolumes([]string{"vol1", "missing", "vol1"}, []Volume{v1})
		require.NoError(t, err)
		require.Len(t, res, 3)
		assert.Equal(t, v1.Mountpoint, res[0].Mountpoint)
		assert.Equal(t, "missing", res[1].Name)
		assert.Empty(t, res[1].Mountpoint)
		assert.Equal(t, v1.Mountpoint, res[2].Mountpoint)
	})

	t.Run("unmatched volume in found returns error", func(t *testing.T) {
		t.Parallel()

		unmatched := Volume{Name: "unmatched-vol"}
		res, err := alignVolumes([]string{"vol1"}, []Volume{v1, unmatched})
		require.Error(t, err)
		assert.Nil(t, res)
	})
}

func TestParseVolumeSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected uint64
		hasError bool
	}{
		{input: "", expected: 0},
		{input: "-1B", expected: 0},
		{input: "-10MB", expected: 0},
		{input: "0B", expected: 0},
		{input: "10B", expected: 10},
		{input: "1kB", expected: 1000},
		{input: "1MB", expected: 1000 * 1000},
		{input: "invalid", hasError: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			val, err := parseVolumeSize(tt.input)
			if tt.hasError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, val)
		})
	}
}

func TestIsTransientDockerDfError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err      error
		name     string
		stderr   string
		expected bool
	}{
		{
			name: "rw layer snapshot not found in stderr",
			stderr: "Error response from daemon: failed to retrieve container list: " +
				"rw layer snapshot not found for container abc123",
			expected: true,
		},
		{
			name:     "failed to retrieve container list in err",
			err:      errors.New("command failed: failed to retrieve container list: error"),
			expected: true,
		},
		{
			name:     "unrelated error",
			stderr:   "cannot connect to docker daemon",
			err:      errors.New("exit status 1"),
			expected: false,
		},
		{
			name:     "nil outputs",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var cco *commandContextOutput
			if tt.stderr != "" {
				cco = &commandContextOutput{}
				cco.Stderr.WriteString(tt.stderr)
			}

			res := isTransientDockerDfError(cco, tt.err)
			assert.Equal(t, tt.expected, res)
		})
	}
}
