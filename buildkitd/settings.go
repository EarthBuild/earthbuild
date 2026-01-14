package buildkitd

import (
	"strconv"
	"strings"
	"time"

	"github.com/mitchellh/hashstructure/v2"
	"github.com/pkg/errors"
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
		return "", errors.Wrap(err, "hash settings")
	}

	return strconv.FormatUint(hash, 16), nil
}

// VerifyHash checks whether a given hash matches the settings.
func (s Settings) VerifyHash(hash string) (bool, error) {
	newHash, err := hashstructure.Hash(s, hashstructure.FormatV2, nil)
	if err != nil {
		return false, errors.Wrap(err, "hash settings")
	}

	oldHash, err := strconv.ParseUint(strings.TrimSpace(hash), 16, 64)
	if err != nil {
		return false, errors.Wrap(err, "parse hash")
	}

	return oldHash == newHash, nil
}

// HasConfiguredCacheSize returns if the buildkitd cache size was configured.
func (s Settings) HasConfiguredCacheSize() bool {
	return s.CacheSizeMb > 0 || s.CacheSizePct > 0
}
