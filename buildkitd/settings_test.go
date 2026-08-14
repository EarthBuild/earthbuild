package buildkitd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsHash(t *testing.T) {
	t.Parallel()

	base := Settings{
		TLSCA:                "ca-cert",
		ServerTLSCert:        "server-cert",
		IPTables:             "iptables",
		StartUpLockPath:      "/tmp/lock",
		BuildkitAddress:      "tcp://1.2.3.4",
		LocalRegistryAddress: "localhost:5000",
		ServerTLSKey:         "server-key",
		ClientTLSKey:         "client-key",
		ClientTLSCert:        "client-cert",
		VolumeName:           "my-vol",
		AdditionalConfig:     "some-config",
		AdditionalArgs:       []string{"--debug"},
		CacheSizeMb:          1024,
		CacheSizePct:         50,
		Timeout:              10 * time.Second,
		MaxParallelism:       4,
		CacheKeepDuration:    3600,
		CniMtu:               1500,
		UseTLS:               true,
		UseTCP:               true,
		EnableProfiler:       true,
		NoUpdate:             true,
		Debug:                true,
	}

	baseHash, err := base.Hash()
	require.NoError(t, err, "unexpected error hashing base settings")

	const wantBaseHash = "90891425e8e43a1"
	assert.Equal(t, wantBaseHash, baseHash, "base settings hash has changed")

	tests := []struct {
		modify      func(*Settings)
		name        string
		wantChanged bool
		wantVerify  bool
	}{
		{
			name:        "Deterministic (identical struct)",
			modify:      func(*Settings) {},
			wantChanged: false,
			wantVerify:  true,
		},
		{
			name: "TLSCA change",
			modify: func(s *Settings) {
				s.TLSCA = "other-ca-cert"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "ServerTLSCert change",
			modify: func(s *Settings) {
				s.ServerTLSCert = "other-server-cert"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "IPTables change",
			modify: func(s *Settings) {
				s.IPTables = "other-iptables"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "StartUpLockPath change (ignored)",
			modify: func(s *Settings) {
				s.StartUpLockPath = "/tmp/other-lock"
			},
			wantChanged: false,
			wantVerify:  true,
		},
		{
			name: "BuildkitAddress change",
			modify: func(s *Settings) {
				s.BuildkitAddress = "tcp://5.6.7.8"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "LocalRegistryAddress change",
			modify: func(s *Settings) {
				s.LocalRegistryAddress = "localhost:6000"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "ServerTLSKey change",
			modify: func(s *Settings) {
				s.ServerTLSKey = "other-server-key"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "ClientTLSKey change",
			modify: func(s *Settings) {
				s.ClientTLSKey = "other-client-key"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "ClientTLSCert change",
			modify: func(s *Settings) {
				s.ClientTLSCert = "other-client-cert"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "VolumeName change",
			modify: func(s *Settings) {
				s.VolumeName = "other-vol"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "AdditionalConfig change",
			modify: func(s *Settings) {
				s.AdditionalConfig = "other-config"
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "AdditionalArgs change",
			modify: func(s *Settings) {
				s.AdditionalArgs = []string{"--debug", "--verbose"}
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "CacheSizeMb change",
			modify: func(s *Settings) {
				s.CacheSizeMb = 2048
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "CacheSizePct change",
			modify: func(s *Settings) {
				s.CacheSizePct = 75
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "Timeout change (ignored)",
			modify: func(s *Settings) {
				s.Timeout = 20 * time.Second
			},
			wantChanged: false,
			wantVerify:  true,
		},
		{
			name: "MaxParallelism change",
			modify: func(s *Settings) {
				s.MaxParallelism = 8
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "CacheKeepDuration change",
			modify: func(s *Settings) {
				s.CacheKeepDuration = 7200
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "CniMtu change",
			modify: func(s *Settings) {
				s.CniMtu = 1450
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "UseTLS change",
			modify: func(s *Settings) {
				s.UseTLS = false
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "UseTCP change",
			modify: func(s *Settings) {
				s.UseTCP = false
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "EnableProfiler change",
			modify: func(s *Settings) {
				s.EnableProfiler = false
			},
			wantChanged: true,
			wantVerify:  false,
		},
		{
			name: "NoUpdate change (ignored)",
			modify: func(s *Settings) {
				s.NoUpdate = false
			},
			wantChanged: false,
			wantVerify:  true,
		},
		{
			name: "Debug change",
			modify: func(s *Settings) {
				s.Debug = false
			},
			wantChanged: true,
			wantVerify:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := base
			tt.modify(&s)

			hash, err := s.Hash()
			require.NoError(t, err)

			hasChanged := (hash != baseHash)
			assert.Equal(t, tt.wantChanged, hasChanged, "changed status mismatch (baseHash: %q, modHash: %q)", baseHash, hash)

			ok, err := s.VerifyHash(baseHash)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVerify, ok, "VerifyHash status mismatch")
		})
	}
}

func BenchmarkSettingsHash(b *testing.B) {
	s := Settings{
		TLSCA:                "ca-cert",
		ServerTLSCert:        "server-cert",
		IPTables:             "iptables",
		StartUpLockPath:      "/tmp/lock",
		BuildkitAddress:      "tcp://1.2.3.4",
		LocalRegistryAddress: "localhost:5000",
		ServerTLSKey:         "server-key",
		ClientTLSKey:         "client-key",
		ClientTLSCert:        "client-cert",
		VolumeName:           "my-vol",
		AdditionalConfig:     "some-config",
		AdditionalArgs:       []string{"--verbose"},
		CacheSizeMb:          1024,
		CacheSizePct:         50,
		Timeout:              10 * time.Second,
		MaxParallelism:       4,
		CacheKeepDuration:    3600,
		CniMtu:               1500,
		UseTLS:               true,
		UseTCP:               true,
		EnableProfiler:       true,
		NoUpdate:             true,
		Debug:                true,
	}

	for b.Loop() {
		_, err := s.Hash()
		if err != nil {
			b.Fatal(err)
		}
	}
}
