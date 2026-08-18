package container

import (
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

		urls, err := ResolveEndpoints(DriverDockerShell, &Config{
			BuildkitHostCLIValue:       tt.args.buildkit,
			BuildkitHostFileValue:      tt.config.BuildkitHost,
			LocalRegistryHostFileValue: tt.config.LocalRegistryHost,
			LocalContainerName:         "test", //nolint:goconst
			DefaultPort:                8372,
			Console:                    logger,
		})
		r.NoError(err)
		assert.Equal(t, tt.expected, results{
			buildkit:      urls.BuildkitHost.String(),
			localRegistry: urls.LocalRegistryHost.String(),
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

		_, err := ResolveEndpoints(DriverDockerShell, &Config{
			BuildkitHostFileValue:      tt.config.BuildkitHost,
			LocalRegistryHostFileValue: tt.config.LocalRegistryHost,
			Console:                    logger,
			LocalContainerName:         "test",
			DefaultPort:                8372,
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
		_, err := ParseURL(tt.url)
		assert.ErrorIs(t, err, tt.expected)
	}
}

func TestParseURL(t *testing.T) {
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
		_, err := ParseURL(tt.url)
		assert.NoError(t, err)
	}
}

func TestBuildArgMatrixValidationNonIssues(t *testing.T) {
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
			log: "Buildkit and Local Registry URLs are pointed at different hosts",
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

		_, err := ResolveEndpoints(DriverDockerShell, &Config{
			BuildkitHostFileValue:      tt.config.BuildkitHost,
			LocalRegistryHostFileValue: tt.config.LocalRegistryHost,
			Console:                    logger,
			LocalContainerName:         "test",
			DefaultPort:                8372,
		})
		r.NoError(err)
		assert.NotContains(t, logs.String(), tt.log)
	}
}

func BenchmarkIsLocal(b *testing.B) {
	addrs := []string{
		"docker-container://earthly-buildkitd",
		"podman-container://earthly-buildkitd",
		"apple-container://earthly-buildkitd",
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
