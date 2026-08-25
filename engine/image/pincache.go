package image

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Pins remembers what a mutable reference resolved to, between builds.
//
// **A build with nothing to do is almost entirely this.** `plan` is 0.664s of a
// 0.69s no-op `+earthly`, and all of it is one token exchange and one manifest
// fetch per reference, before a single step runs (E550, E703). Two builds a
// minute apart get the same answers, and nothing remembered them: the token
// cache is per process and dies with it.
//
// On disk beside the challenges, which is where "who issues tokens for this
// registry" already lives (E535) and for the same reason - it is a property of
// the machine rather than of one build. Unlike a token this is not a credential:
// it is a digest a registry published, so it can be written down.
//
// **The window is the whole of the trade.** A tag that moves is not noticed
// until the pin expires. That is a real change to which image a build gets, so
// it is off unless a window is asked for, and a stale pin is still strictly
// better than the alternative when resolution fails - which is to use the
// reference as written and get no pinning at all.
type Pins struct {
	dir string
	ttl time.Duration
}

// NewPins remembers pins under dir for ttl. A ttl of zero is off: it neither
// reads nor writes, so turning the setting off turns the behaviour off.
func NewPins(dir string, ttl time.Duration) *Pins {
	return &Pins{dir: filepath.Join(dir, "pins"), ttl: ttl}
}

// Get is what this reference resolved to within the window, if anything did.
func (p *Pins) Get(ref, platform string) (string, bool) {
	if p == nil || p.ttl <= 0 || p.dir == "" {
		return "", false
	}

	b, err := os.ReadFile(p.at(ref, platform))
	if err != nil {
		return "", false
	}

	// `digest\nunix-seconds`. Written by this engine, so anything else is a
	// file that is not ours and is ignored rather than repaired.
	to, when, ok := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if !ok || to == "" {
		return "", false
	}

	secs, err := strconv.ParseInt(strings.TrimSpace(when), 10, 64)
	if err != nil {
		return "", false
	}

	// **Measured from when it was written, not from when it is read.** The
	// window belongs to the answer's age; reading it does not make it younger.
	if time.Since(time.Unix(secs, 0)) > p.ttl {
		return "", false
	}

	return to, true
}

// Put records what a reference resolved to, best effort.
//
// A pin that could not be written costs the round trip it would have saved next
// time and nothing else, which is what happened before any of this existed.
func (p *Pins) Put(ref, platform, to string) {
	if p == nil || p.ttl <= 0 || p.dir == "" || to == "" {
		return
	}

	err := os.MkdirAll(p.dir, 0o750)
	if err != nil {
		return
	}

	_ = os.WriteFile(p.at(ref, platform),
		[]byte(to+"\n"+strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o600)
}

// at is where one reference's pin lives.
//
// **Keyed on the pair.** The same tag on two platforms names two manifests, and
// collapsing them would pin one platform's image for both.
func (p *Pins) at(ref, platform string) string {
	sum := sha256.Sum256([]byte(ref + "\x00" + platform))

	return filepath.Join(p.dir, hex.EncodeToString(sum[:]))
}

// EnvPinTTL is how long a resolved reference may be reused without asking the
// registry again.
//
// A Go duration - `5m`, `90s`. Empty, zero, negative or unparseable is off,
// which is the default: a window is a period during which a tag that has moved
// is not noticed, and that changes which image a build gets. Nobody should get
// it without having asked.
//
// What it buys: `plan` is 0.664s of a 0.69s no-op build and almost all of it is
// this (E703).
const EnvPinTTL = "EARTH_PIN_TTL"

// PinTTLFromEnv reads EnvPinTTL. Anything that is not a positive duration is
// off, because a mistyped setting must not quietly buy a staleness window.
func PinTTLFromEnv() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(EnvPinTTL)))
	if err != nil || d <= 0 {
		return 0
	}

	return d
}
