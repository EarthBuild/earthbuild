package cache

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Profiles remembers what each class of step read, so Κ₂ can be computed before
// the step runs. Implements core.Profiles.
//
// **Everything here degrades to "no prediction".** A profile is a hint and
// `Consistent` is what makes acting on one safe (green paper 4.7), so a store
// that cannot be read costs a rebuild, while one that returns something partial
// costs correctness: fewer paths than the step actually read is precisely the
// false-hit shape I3 exists to prevent. A file that does not parse whole is
// therefore discarded whole, and there is no path in this file that returns a
// partial observation.
//
// One file per class, named by the class key, inserted by rename - the same
// arrangement the action cache uses and for the same reason: several steps of
// one class finish at once, and a reader must see the old file or the new one
// rather than half of either.
type Profiles struct{ dir string }

// OpenProfiles prepares a profile store under root.
//
// Reports a directory it cannot use rather than degrading silently, because a
// build quietly running without its L2 tier is a build whose speed nobody can
// account for (I11).
func OpenProfiles(root string) (*Profiles, error) {
	dir := filepath.Join(root, "profiles")

	// 0750, as the action cache is: a profile is a list of the paths a
	// developer's builds read, which is a description of what they work on.
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return nil, fmt.Errorf("prepare the profile store at %s: %w", dir, err)
	}

	return &Profiles{dir: dir}, nil
}

// storedProfile is the on-disk form.
//
// Explicit rather than encoding core.Observation directly: this outlives the
// process and has to survive the struct changing. Digests are hex because a
// byte array in JSON is neither readable nor stable.
//
// `Incomplete` has no field on purpose. An incomplete observation is never
// written (see Put), so a format that could express one would be a format that
// could be *read* as complete after a future change to Put - and the reader has
// no way to tell. What cannot be written cannot be misread.
type storedProfile struct {
	Reads    map[string]string `json:"reads,omitempty"`
	Listings map[string]string `json:"listings,omitempty"`
	// The slice last: a map header is one word and a slice is three, so putting
	// it between the maps makes the collector scan the whole struct (govet
	// fieldalignment). JSON is read by name, so the order is not the format.
	Negative []string `json:"negative,omitempty"`
}

func (p *Profiles) path(class core.Key) string {
	return filepath.Join(p.dir, hex.EncodeToString(class[:])+".json")
}

// Get returns what this class of step read last time, if anything readable.
func (p *Profiles) Get(class core.Key) (core.Observation, bool) {
	b, err := os.ReadFile(p.path(class)) // a digest-named path this package made
	if err != nil {
		return core.Observation{}, false
	}

	var s storedProfile

	// Whole or nothing: a truncated file that happens to parse as far as it
	// goes would yield a subset of the paths, which reads as a complete
	// observation of a step that read less than it did.
	err = json.Unmarshal(b, &s)
	if err != nil {
		return core.Observation{}, false
	}

	obs := core.Observation{
		Reads:    make(map[string]ir.NodeID, len(s.Reads)),
		Listings: make(map[string]ir.NodeID, len(s.Listings)),
		Negative: append([]string(nil), s.Negative...),
	}

	for path, digest := range s.Reads {
		id, err := parseID(digest)
		if err != nil {
			return core.Observation{}, false
		}

		obs.Reads[path] = id
	}

	for path, digest := range s.Listings {
		id, err := parseID(digest)
		if err != nil {
			return core.Observation{}, false
		}

		obs.Listings[path] = id
	}

	return obs, true
}

// Put records what a step of this class read.
//
// An observation the source admitted was lossy is dropped here as well as in
// the scheduler. The scheduler's check is about *this* build; this one is about
// every later build, where nothing remembers where the profile came from - and
// a rule applied at one of the two places it holds has been the session's
// recurring defect.
//
// Failures are silent, which is the one place in this file that deserves an
// argument. A profile that cannot be written costs the next build a prediction;
// reporting it would fail a build over a hint. `Consistent` is what stands
// between a bad prediction and a bad result, and it does not depend on this
// having succeeded.
func (p *Profiles) Put(class core.Key, obs core.Observation) {
	if obs.Incomplete {
		return
	}

	s := storedProfile{
		Reads:    make(map[string]string, len(obs.Reads)),
		Listings: make(map[string]string, len(obs.Listings)),
		Negative: uniqueSorted(obs.Negative),
	}

	for path, id := range obs.Reads {
		s.Reads[path] = id.String()
	}

	for path, id := range obs.Listings {
		s.Listings[path] = id.String()
	}

	// Go's encoder sorts map keys, so two machines that learned the same thing
	// hold identical bytes - which is what lets a fleet recognise one profile
	// rather than two (Appendix C).
	b, err := json.Marshal(s)
	if err != nil {
		return
	}

	// Written beside its destination and renamed. Several steps of one class
	// finish at once, and a reader catching a partial write would get a subset
	// of the paths: it parses, it looks complete, and it is the false hit.
	tmp, err := os.CreateTemp(p.dir, ".profile-*")
	if err != nil {
		return
	}

	name := tmp.Name()

	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		_ = os.Remove(name)

		return
	}

	err = os.Chmod(name, 0o600)
	if err != nil {
		_ = os.Remove(name)

		return
	}

	err = os.Rename(name, p.path(class))
	if err != nil {
		_ = os.Remove(name)
	}
}

// uniqueSorted is a slice as the set it represents.
//
// The same normalisation DeriveObservedKey applies, applied here so that what
// is stored and what is keyed cannot disagree: a profile holding a repeat would
// derive one key when read and another when the observation was fresh.
func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	out := append([]string(nil), in...)
	sort.Strings(out)

	kept := out[:1]

	for _, s := range out[1:] {
		if s != kept[len(kept)-1] {
			kept = append(kept, s)
		}
	}

	return kept
}
