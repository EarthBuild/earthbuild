package app

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth/base"
	"github.com/EarthBuild/earthbuild/config"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/stretchr/testify/require"
)

const undetectedMsg = "Unable to detect Docker, Podman, or Apple Container"

// An explicit buildkit host must be honoured even when no container frontend
// can be found on the client: a remote buildkitd needs no Docker or Podman
// locally (#863).
func TestParseEngineExplicitBuildkitHost(t *testing.T) {
	t.Parallel()

	const (
		remoteHost = "tcp://buildkit.example.com:8372"
		flagHost   = "tcp://flag.example.com:9000"
		localHost  = "tcp://127.0.0.1:8372"
		privHost   = "tcp://10.0.0.5:8372"
	)

	for _, tc := range []struct {
		name            string
		flagHost        string
		cfgHost         string
		wantHost        string
		detect          bool
		wantUndetectMsg bool
	}{
		{
			name:     "config host, detection fails",
			cfgHost:  remoteHost,
			detect:   true,
			wantHost: remoteHost,
		},
		{
			name:     "config host, detection skipped",
			cfgHost:  remoteHost,
			wantHost: remoteHost,
		},
		{
			name:     "flag host wins over config host",
			flagHost: flagHost,
			cfgHost:  remoteHost,
			detect:   true,
			wantHost: flagHost,
		},
		{
			name:            "no explicit host, detection fails",
			detect:          true,
			wantUndetectMsg: true,
		},
		{
			name:            "explicit local host, detection fails",
			cfgHost:         localHost,
			detect:          true,
			wantHost:        localHost,
			wantUndetectMsg: true,
		},
		{
			// A private-network host is another machine, so no local frontend
			// is expected; the stub connects to it as given.
			name:     "explicit private network host, detection fails",
			cfgHost:  privHost,
			detect:   true,
			wantHost: privHost,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			log := conslogging.New(&out, nil, 0, conslogging.Info, false)
			cli := base.NewCLI(log)
			cli.SetCfg(&config.Config{Global: config.GlobalConfig{
				// An unknown frontend makes detection fail without depending
				// on what is installed on the machine running the test.
				Engine:       "no-such-frontend",
				BuildkitHost: tc.cfgHost,
			}})
			cli.Flags().InstallationName = "earth"
			cli.Flags().ContainerName = "earth-buildkitd"
			cli.Flags().BuildkitHost = tc.flagHost

			app := &EarthApp{BaseCLI: cli}
			require.NoError(t, app.parseEngine(t.Context(), tc.detect))

			require.Equal(t, "Stub", cli.Flags().Engine.Metadata().Name)
			require.Equal(t, tc.wantHost, cli.Flags().BuildkitHost)
			require.Empty(t, cli.Flags().LocalRegistryHost)

			if tc.wantUndetectMsg {
				require.Contains(t, out.String(), undetectedMsg)
			} else {
				require.NotContains(t, out.String(), undetectedMsg)
			}
		})
	}
}

func TestOnThisMachine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		addr string
		want bool
	}{
		{addr: "docker-container://earth-buildkitd", want: true},
		{addr: "tcp://127.0.0.1:8372", want: true},
		{addr: "tcp://localhost:8372", want: true},
		{addr: "tcp://[::1]:8372", want: true},
		{addr: "tcp://10.0.0.5:8372", want: false},
		{addr: "tcp://192.168.1.20:8372", want: false},
		{addr: "tcp://172.16.4.2:8372", want: false},
		{addr: "tcp://buildkit.example.com:8372", want: false},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, onThisMachine(tc.addr))
		})
	}
}
