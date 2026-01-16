package containerutil

import (
	"context"
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
		ctx := context.Background()

		var logs strings.Builder

		logger := conslogging.Current(conslogging.NoColor, conslogging.DefaultPadding, conslogging.Info, false)
		logger = logger.WithWriter(&logs)

		frontend, err := NewStubFrontend(ctx, &FrontendConfig{
			LocalContainerName: "test-stub",
		})
		r.NoError(err)

		stub, ok := frontend.(*stubFrontend)
		assert.True(t, ok)

		urls, err := stub.setupAndValidateAddresses(FrontendDockerShell, &FrontendConfig{
			BuildkitHostCLIValue:       tt.args.buildkit,
			BuildkitHostFileValue:      tt.config.BuildkitHost,
			LocalRegistryHostFileValue: tt.config.LocalRegistryHost,
			LocalContainerName:         "test",
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
			expected: errURLParseFailure,
			log:      "",
		},
		{
			testName: "Invalid registry URL",
			config: config.GlobalConfig{
				BuildkitHost:      "",
				LocalRegistryHost: "http\r://foo.com/",
			},
			expected: errURLParseFailure,
			log:      "",
		},
		{
			testName: "Homebrew test",
			config: config.GlobalConfig{
				BuildkitHost:      "127.0.0.1",
				LocalRegistryHost: "",
			},
			expected: errURLValidationFailure,
			log:      "",
		},
	}

	for _, tt := range tests {
		ctx := context.Background()

		var logs strings.Builder

		logger := conslogging.Current(conslogging.NoColor, conslogging.DefaultPadding, conslogging.Info, false)
		logger = logger.WithWriter(&logs)

		frontend, err := NewStubFrontend(ctx, &FrontendConfig{
			LocalContainerName: "test-stub",
		})
		r.NoError(err)

		stub, ok := frontend.(*stubFrontend)
		assert.True(t, ok)

		_, err = stub.setupAndValidateAddresses(FrontendDockerShell, &FrontendConfig{
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

func TestParseAndValidateURLFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expected error
		testName string
		url      string
	}{
		{
			testName: "Invalid URL",
			url:      "http\r://foo.com/",
			expected: errURLParseFailure,
		},
		{
			testName: "Invalid Scheme",
			url:      "gopher://my-hole",
			expected: errURLValidationFailure,
		},
		{
			testName: "Missing Port",
			url:      "tcp://my-server",
			expected: errURLValidationFailure,
		},
	}

	for _, tt := range tests {
		_, err := parseAndValidateURL(tt.url)
		assert.ErrorIs(t, err, tt.expected)
	}
}

func TestParseAndValidateURL(t *testing.T) {
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
		_, err := parseAndValidateURL(tt.url)
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
		ctx := context.Background()

		var logs strings.Builder

		logger := conslogging.Current(conslogging.NoColor, conslogging.DefaultPadding, conslogging.Info, false)
		logger = logger.WithWriter(&logs)

		frontend, err := NewStubFrontend(ctx, &FrontendConfig{
			LocalContainerName: "test-stub",
		})
		r.NoError(err)

		stub, ok := frontend.(*stubFrontend)
		assert.True(t, ok)

		_, err = stub.setupAndValidateAddresses(FrontendDockerShell, &FrontendConfig{
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
