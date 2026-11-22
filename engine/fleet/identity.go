package fleet

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrNoSecret is a derivation attempted from public metadata alone.
//
// C.1 makes the `secret` term normative and says why: a run identifier is
// visible on a public repository, so a key derived without a secret can be
// derived by **any observer**, who then joins the mesh and serves results. The
// refusal is here rather than in a caller because there is no honest reason to
// want the weaker key.
var ErrNoSecret = errors.New("a driver key needs a secret, not only public metadata")

// Session is the public half of what a driver key is derived from.
//
// Every field of it is visible to anyone watching a public repository, which is
// the point: they are here to make the key *specific*, and the secret is what
// makes it *unguessable*. Separating them in the type keeps the second from
// being forgotten while the first looks sufficient.
type Session struct {
	// Session names the build session.
	Session string
	// RunID is the CI run, or whatever identifies this invocation.
	RunID string
	// Attempt distinguishes a retry from the run it retries, so a re-run does
	// not join the previous attempt's mesh.
	Attempt int
	// Repo is the repository being built.
	Repo string
}

// DeriveDriverKey is (C.1): 𝑘 ≡ HKDF(session ‖ run_id ‖ attempt ‖ repo ‖ secret).
//
// The concatenation is the canonical encoding rather than a join, and that is
// not tidiness: `‖` over raw strings lets ("ab", "c") and ("a", "bc") derive the
// **same key**, so two different sessions would share a mesh. `ir.Encoder`
// length-prefixes, which is the same 𝒮 that keeps two distinct steps from
// sharing a cache key (§1.4, B.1).
//
// The secret is the HKDF *key* and the public terms are its info, which is the
// right way round: info is a domain separator and may be public, while the
// entropy has to come from the secret.
func DeriveDriverKey(s Session, secret []byte) (ed25519.PrivateKey, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("%w"+
			"\n  a run identifier is visible on a public repository, so a key"+
			" derived without a secret can be derived by anyone watching",
			ErrNoSecret)
	}

	var info bytes.Buffer

	e := ir.NewEncoder(&info)
	e.Str(s.Session)
	e.Str(s.RunID)
	e.Count(s.Attempt)
	e.Str(s.Repo)

	seed, err := hkdf.Key(sha256.New, secret, nil, info.String(), ed25519.SeedSize)
	if err != nil {
		return nil, fmt.Errorf("derive the driver key: %w", err)
	}

	return ed25519.NewKeyFromSeed(seed), nil
}

// Allowlist is the set of worker identities a driver will talk to.
//
// C.1: "The driver additionally publishes an allowlist of worker identities and
// refuses others." Deriving the key is therefore necessary and **not
// sufficient** - which is the point of having both, since a secret can leak and
// an allowlist can be narrowed without rotating one.
type Allowlist struct{ allowed map[string]bool }

// NewAllowlist admits exactly these identities.
func NewAllowlist(workers ...ed25519.PublicKey) *Allowlist {
	a := &Allowlist{allowed: make(map[string]bool, len(workers))}
	for _, w := range workers {
		a.allowed[string(w)] = true
	}

	return a
}

// Allows reports whether this identity may join.
//
// An empty allowlist admits **nobody**, which is the safe direction and the
// opposite of the usual convention that an empty filter matches everything. A
// driver that forgot to publish one talks to no worker rather than to any.
func (a *Allowlist) Allows(w ed25519.PublicKey) bool {
	if a == nil || len(w) == 0 {
		return false
	}

	return a.allowed[string(w)]
}
