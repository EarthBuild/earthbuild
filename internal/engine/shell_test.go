package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

//nolint:lll
const containerListText = `7a3c4741b86e,earthly-darwin-proxy-T58UvV,Up 5 hours,alpine/socat:1.7.4.4,2024-01-23 12:53:32 -0800 PST
d8183461827c,earthly-dev-buildkitd,Up 5 hours,earthly/buildkitd:dev-main,2024-01-23 12:50:02 -0800 PST
3084cac7996e,earthly-buildkitd,Up 6 hours,earthly/buildkitd:prerelease,2024-01-23 12:31:06 -0800 PST`

func Test_parseContainerList(t *testing.T) {
	t.Parallel()

	ret, err := parseContainerList(containerListText)
	r := require.New(t)
	r.NoError(err)
	r.Len(ret, 3)
	r.Equal("7a3c4741b86e", ret[0].ID)
	r.Equal("earthly-darwin-proxy-T58UvV", ret[0].Name)
	r.Equal(StatusRunning, ret[0].Status)
	r.Equal("alpine/socat:1.7.4.4", ret[0].Image)
	r.Equal(int64(1706043212), ret[0].Created.Unix())
}

func Test_parseContainerList_whitespace(t *testing.T) {
	t.Parallel()

	ret, err := parseContainerList(containerListText + "\n\n")
	r := require.New(t)
	r.NoError(err)
	r.Len(ret, 3)
}

func Test_parseContainerList_empty(t *testing.T) {
	t.Parallel()

	ret, err := parseContainerList("\n\n")
	r := require.New(t)
	r.NoError(err)
	r.Empty(ret)
}

func TestNormalizeContainerStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"running", StatusRunning},
		{"RUNNING", StatusRunning},
		{"Up 5 hours", StatusRunning},
		{"Up 10 seconds (healthy)", StatusRunning},
		{"exited", StatusExited},
		{"EXITED", StatusExited},
		{"stopped", StatusExited},
		{"dead", StatusDead},
		{"created", StatusCreated},
		{"paused", StatusPaused},
		{"restarting", StatusRestarting},
		{"removing", StatusRemoving},
		{"unknown-custom", "unknown-custom"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeContainerStatus(tt.input))
		})
	}
}

func TestRedactArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		args []string
	}{
		{
			name: "empty",
			args: nil,
			want: "",
		},
		{
			name: "no secrets",
			args: []string{"docker", "run", "-d", "--name", "my-container", "-p", "8372:8372", "alpine:latest"},
			want: "docker run -d --name my-container -p 8372:8372 alpine:latest",
		},
		{
			name: "env flag split",
			args: []string{"docker", "run", "-e", "FOO=secret1", "--env", "BAR=secret2", "image"},
			want: "docker run -e FOO=[REDACTED] --env BAR=[REDACTED] image",
		},
		{
			name: "env flag combined",
			args: []string{"docker", "run", "-e=FOO=secret1", "--env=BAR=secret2", "image"},
			want: "docker run -e=FOO=[REDACTED] --env=BAR=[REDACTED] image",
		},
		{
			name: "bare env var without value",
			args: []string{"docker", "run", "-e", "PASSTHROUGH", "image"},
			want: "docker run -e [REDACTED] image",
		},
		{
			name: "secret flag",
			args: []string{"docker", "build", "--secret", "id=token,src=/path", "image"},
			want: "docker build --secret id=[REDACTED] image",
		},
		{
			name: "password flag",
			args: []string{"docker", "login", "--password", "supersecret", "-u", "admin"},
			want: "docker login --password [REDACTED] -u admin",
		},
		{
			name: "password combined",
			args: []string{"docker", "login", "--password=supersecret"},
			want: "docker login --password=[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, redactArgs(tt.args))
		})
	}
}

func TestIsShellResourceNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		outputStderr string
		err          error
		resourceType string
		want         bool
	}{
		{
			name:         "no error",
			err:          nil,
			resourceType: "container",
			want:         false,
		},
		{
			name:         "docker container not found",
			outputStderr: "Error response from daemon: No such container: c1",
			err:          errors.New("exit status 1"),
			resourceType: "container",
			want:         true,
		},
		{
			name:         "podman container not found",
			outputStderr: "Error: no such container \"c1\"",
			err:          errors.New("exit status 125"),
			resourceType: "container",
			want:         true,
		},
		{
			name:         "docker image not found",
			outputStderr: "Error response from daemon: No such image: img1:latest",
			err:          errors.New("exit status 1"),
			resourceType: "image",
			want:         true,
		},
		{
			name:         "podman image not found",
			outputStderr: "Error: no such image \"img1\"",
			err:          errors.New("exit status 125"),
			resourceType: "image",
			want:         true,
		},
		{
			name:         "daemon connection error",
			outputStderr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
			err:          errors.New("exit status 1"),
			resourceType: "container",
			want:         false,
		},
		{
			name:         "permission denied",
			outputStderr: "permission denied while trying to connect to the Docker daemon socket",
			err:          errors.New("exit status 1"),
			resourceType: "container",
			want:         false,
		},
		{
			name:         "context canceled",
			outputStderr: "",
			err:          context.Canceled,
			resourceType: "container",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			output := &commandContextOutput{}
			output.Stderr.WriteString(tt.outputStderr)
			got := isShellResourceNotFound(output, tt.err, tt.resourceType)
			require.Equal(t, tt.want, got)
		})
	}
}

