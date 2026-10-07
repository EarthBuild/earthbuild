package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsStub(t *testing.T) {
	t.Parallel()

	stub, err := NewStub(&Config{})
	require.NoError(t, err)

	var nilClient *Client

	require.True(t, stub.IsStub())
	require.True(t, nilClient.IsStub())
	require.False(t, NewTestClient(Metadata{Name: "Docker"}).IsStub())
}

// With the stub engine (no Docker, Podman or Apple Container) earth has no way
// to start a buildkitd, so even an address IsLocal accepts, such as one on a
// private network, is a daemon to connect to rather than manage (#863).
func TestManagesDaemon(t *testing.T) {
	t.Parallel()

	stub, err := NewStub(&Config{})
	require.NoError(t, err)

	dockerEng := NewTestClient(Metadata{Name: "Docker"})

	const (
		loopback  = "tcp://127.0.0.1:8372"
		private10 = "tcp://10.0.0.5:8372"
		public    = "tcp://buildkit.example.com:8372"
	)

	for _, tc := range []struct {
		eng  *Client
		name string
		addr string
		want bool
	}{
		{name: "real engine, container scheme", addr: DockerSchemePrefix + "earth-buildkitd", eng: dockerEng, want: true},
		{name: "real engine, loopback", addr: loopback, eng: dockerEng, want: true},
		{name: "real engine, private 10.x", addr: private10, eng: dockerEng, want: true},
		{name: "real engine, public host", addr: public, eng: dockerEng, want: false},
		{name: "stub, private 10.x", addr: private10, eng: stub, want: false},
		{name: "stub, private 192.168.x", addr: "tcp://192.168.1.20:8372", eng: stub, want: false},
		{name: "stub, private 172.16.x", addr: "tcp://172.16.4.2:8372", eng: stub, want: false},
		{name: "stub, loopback", addr: loopback, eng: stub, want: false},
		{name: "stub, public host", addr: public, eng: stub, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, ManagesDaemon(tc.eng, tc.addr))
		})
	}
}
