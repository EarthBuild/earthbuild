package buildkitd

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"strconv"
	"time"
)

// Settings represents the buildkitd settings used to start up the daemon with.
type Settings struct {
	TLSCA                string
	ServerTLSCert        string
	IPTables             string
	StartUpLockPath      string // StartUpLockPath is not included in hash.
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
	Timeout              time.Duration // Timeout is not included in hash.
	MaxParallelism       int
	CacheKeepDuration    int
	CniMtu               uint16
	UseTLS               bool
	UseTCP               bool
	EnableProfiler       bool
	NoUpdate             bool // NoUpdate is not included in hash.
	Debug                bool
}

type settingsHasher struct {
	h   hash.Hash64
	err error
	buf [64]byte // buf is used to format integers, booleans, and delimiters without heap allocations.
}

func (sh *settingsHasher) writeString(str string) {
	if sh.err != nil {
		return
	}

	_, sh.err = io.WriteString(sh.h, str)
	if sh.err != nil {
		return
	}

	sh.buf[0] = 0
	_, sh.err = sh.h.Write(sh.buf[:1])
}

func (sh *settingsHasher) writeBool(b bool) {
	if sh.err != nil {
		return
	}

	val := byte(0)
	if b {
		val = 1
	}

	sh.buf[0] = val
	_, sh.err = sh.h.Write(sh.buf[:1])
}

func (sh *settingsHasher) writeInt(i int) {
	if sh.err != nil {
		return
	}

	// #nosec G115 -- safe conversion of int to uint64 for hashing purposes
	binary.BigEndian.PutUint64(sh.buf[:8], uint64(i))
	sh.buf[8] = 0
	_, sh.err = sh.h.Write(sh.buf[:9])
}

// Hash returns a secure hash of the settings.
func (s Settings) Hash() (string, error) {
	sh := &settingsHasher{h: fnv.New64a()}

	sh.writeString(s.TLSCA)
	sh.writeString(s.ServerTLSCert)
	sh.writeString(s.IPTables)
	sh.writeString(s.BuildkitAddress)
	sh.writeString(s.LocalRegistryAddress)
	sh.writeString(s.ServerTLSKey)
	sh.writeString(s.ClientTLSKey)
	sh.writeString(s.ClientTLSCert)
	sh.writeString(s.VolumeName)
	sh.writeString(s.AdditionalConfig)

	for _, arg := range s.AdditionalArgs {
		sh.writeString(arg)
	}

	sh.writeInt(s.CacheSizeMb)
	sh.writeInt(s.CacheSizePct)
	sh.writeInt(s.MaxParallelism)
	sh.writeInt(s.CacheKeepDuration)
	sh.writeInt(int(s.CniMtu))

	sh.writeBool(s.UseTLS)
	sh.writeBool(s.UseTCP)
	sh.writeBool(s.EnableProfiler)
	sh.writeBool(s.Debug)

	if sh.err != nil {
		return "", fmt.Errorf("hash BuildKit setttings: %w", sh.err)
	}

	return strconv.FormatUint(sh.h.Sum64(), 16), nil
}

// VerifyHash checks whether a given hash matches the settings.
func (s Settings) VerifyHash(hash string) (bool, error) {
	newHash, err := s.Hash()
	if err != nil {
		return false, err
	}

	return hash == newHash, nil
}

// HasConfiguredCacheSize returns if the buildkitd cache size was configured.
func (s Settings) HasConfiguredCacheSize() bool {
	return s.CacheSizeMb > 0 || s.CacheSizePct > 0
}
