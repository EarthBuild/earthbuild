package buildkitd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mitchellh/hashstructure/v2"
)

// Settings represents the buildkitd settings used to start up the daemon with.
type Settings struct {
	TLSCA                string
	ServerTLSCert        string
	IPTables             string
	StartUpLockPath      string `hash:"ignore"`
	BuildkitAddress      string
	LocalRegistryAddress string
	ServerTLSKey         string
	ClientTLSKey         string
	ClientTLSCert        string
	VolumeName           string
	AdditionalConfig     string
	AdditionalArgs       []string
	CacheSizeMb          int
	CacheSizePct         int
	Timeout              time.Duration `hash:"ignore"`
	MaxParallelism       int
	CacheKeepDuration    int
	CniMtu               uint16
	UseTLS               bool
	UseTCP               bool
	EnableProfiler       bool
	NoUpdate             bool `hash:"ignore"`
	Debug                bool
}

// Hash returns a secure hash of the settings.
func (s Settings) Hash() (string, error) {
	hash, err := hashstructure.Hash(s, hashstructure.FormatV2, nil)
	if err != nil {
		return "", fmt.Errorf("hash settings: %w", err)
	}

	return strconv.FormatUint(hash, 16), nil
}

// VerifyHash checks whether a given hash matches the settings.
func (s Settings) VerifyHash(hash string) (bool, error) {
	newHash, err := hashstructure.Hash(s, hashstructure.FormatV2, nil)
	if err != nil {
		return false, fmt.Errorf("hash settings: %w", err)
	}

	oldHash, err := strconv.ParseUint(strings.TrimSpace(hash), 16, 64)
	if err != nil {
		return false, fmt.Errorf("parse hash: %w", err)
	}

	return oldHash == newHash, nil
}

// HasConfiguredCacheSize returns if the buildkitd cache size was configured.
func (s Settings) HasConfiguredCacheSize() bool {
	return s.CacheSizeMb > 0 || s.CacheSizePct > 0
}
