package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// shared is a cache mount this step offered, and where its contents sit.
type shared struct {
	mount ir.Mount
	dir   string
}

// shareable is the cache mounts of a step whose contents may cross, with the
// directory each one's contents are in.
//
// **Two flags, and both are required.** `--portable-except` says the contents
// may cross; `--helper` says what crossing *means* - what a unit is, what it is
// called, how two of them merge. A claim with no helper is a cache nothing can
// take apart; a helper with no claim is a cache whose author never offered it.
// Either way the directory stays on this machine, which is what every cache
// mount was before any of this.
//
// Two more never cross whatever they say, for reasons that predate this. An
// **ephemeral** cache is made for the step and removed with it, so there is
// nothing for another machine to be given. A **persisted** one is captured into
// the layer, so its contents *are* the result and travel as one already.
//
// The directory is `<mounts>/<id>/<scope>`, which is the guest's `cacheSource`
// said from this side of the boundary - the scope included, because an unclaimed
// cache has none and a claimed one does, and a reader using the wrong rule finds
// an empty directory and reports an empty cache.
func shareable(mounts []ir.Mount, root, domain string) []shared {
	var out []shared

	for _, m := range mounts {
		if !m.Portable || m.Helper == "" || m.ID == "" || m.Ephemeral || m.Persist {
			continue
		}

		out = append(out, shared{mount: m, dir: filepath.Join(root, m.ID, m.Scope(domain))})
	}

	return out
}

// shareCaches offers each of a step's portable cache mounts to whatever is
// collecting them.
//
// **The executor does not know what a fleet is**, and does not learn it here.
// It knows where the guest keeps a cache mount and which of them the author
// offered; what happens next - a helper, a blob store, a map, a peer - belongs
// to whoever set `Share`, exactly as `Prime` and `Fetch` belong to whoever set
// those.
//
// **A failure here is not a build failure**, and `Share` says so itself. A cache
// that did not cross is a slower build on some other machine; a step failed for
// one is a build that does not finish, and the whole construct is a hint (I11,
// and the same argument every cache miss makes). Reporting belongs to whoever
// set the hook, because that is who has somewhere to report to - the executor
// has no logger and should not grow one for this.
func (e *Executor) shareCaches(ctx context.Context, n *ir.Node) {
	if e.Share == nil || e.Mounts == "" {
		return
	}

	// **The same function that scoped the mount**, not a value passed in beside
	// it. A domain read twice is a domain that can differ twice, and the failure
	// is an export reading a directory the step never wrote - which looks
	// exactly like a cache that is empty.
	withheld := heldBack(n.Op)

	for _, s := range shareable(n.Op.Mounts, e.Mounts, trustDomain()) {
		_ = e.Share(ctx, s.mount, s.dir, withheld)
	}
}

// heldBack says why this step's caches must not cross, or nothing.
//
// **The guarantee this design removed, restored the only way the host can.**
// §C.3 said a cache's contents never leave the machine, so nothing has ever
// scanned one for a credential: `noteSecretLeak` scans a step's *delta*, and
// only for a secret's bytes as the step was handed them. A portable cache breaks
// that promise, and a mount is not a delta.
//
// The host cannot do the scan. `layer.FindSecrets` needs the secret's value,
// which is staged inside the guest and deliberately never reaches this side -
// plumbing it out here to scan with would widen a credential's blast radius to
// fix a problem about credentials. What the host knows is that the step was
// given one, and that is enough for the conservative answer.
//
// Over-cautious for `go build` with a registry token, deliberately. An author
// who wants that cache shared can put the secret in a different step, and "a
// cache from a step that held a credential stays here" is a rule a reader can
// hold in their head, where "we scanned it and think it is fine" is not. The
// cost is a slower build on another machine (I11).
func heldBack(op ir.Op) string {
	switch {
	case len(op.SecretEnv) > 0:
		return "this step was given a secret, and a cache's contents have never" +
			" been scanned for one"

	case op.AWS:
		return "this step was given AWS credentials, and a cache's contents have" +
			" never been scanned for them"

	default:
		return ""
	}
}

// stockCaches offers each of a step's portable cache mounts to whatever can
// fill it, before the step runs.
//
// **The directory need not exist.** A cache nothing has filled here has no
// directory, and that is exactly the case worth stocking - so unlike
// `shareCaches`, which reads what a step left, this hands over a path and lets
// the filler decide whether to make it.
//
// A failure is not a build failure, for `shareCaches`' reason said the other way
// round: a cache that could not be filled is a step that does the work itself,
// which is what every step did before any of this.
func (e *Executor) stockCaches(ctx context.Context, n *ir.Node) {
	if e.Stock == nil || e.Mounts == "" {
		return
	}

	for _, s := range shareable(n.Op.Mounts, e.Mounts, trustDomain()) {
		_ = e.Stock(ctx, s.mount, s.dir)
	}
}

// StockCacheIn asks the guest to fill a cache mount, and ShareCacheIn to file
// what is in one.
//
// **Asked of the guest for `StoreHas`'s reason**, one construct further on: with
// the store on a device the guest owns, the mount is a path the host cannot
// read, a unit is a file the host cannot write, and the helper that knows what a
// unit is has to run where the cache is. A host that tried found an empty
// directory, read it as a mount no step had used, and shared nothing (E-F27).
//
// The scope travels because the host computes it: the claim `--portable-except`
// makes is a property of a mount, the directory is named by an id, and only this
// side has the declaration (see guest.Mount.Scope).
func (e *Executor) StockCacheIn(ctx context.Context, m ir.Mount, scope, at string) error {
	c, err := e.client()
	if err != nil {
		return err
	}

	staged, clean, err := e.stageHelper(ctx, m)
	if err != nil {
		return err
	}

	defer clean()

	return c.StockCache(ctx, cacheMountOf(m, scope), at, staged)
}

// ShareCacheIn files a cache mount's units in the guest and says which map.
func (e *Executor) ShareCacheIn(
	ctx context.Context, m ir.Mount, scope, withheld string,
) (string, error) {
	c, err := e.client()
	if err != nil {
		return "", err
	}

	staged, clean, err := e.stageHelper(ctx, m)
	if err != nil {
		return "", err
	}

	defer clean()

	return c.ShareCache(ctx, cacheMountOf(m, scope), withheld, staged)
}

// stageHelper puts the helper's module where the guest can read it.
//
// **`placeBlob`'s job, which already answers both halves**: shared where the
// sandbox has a filesystem in common with this machine, sent where it has none.
// Reused rather than reinvented, and it is the rule `KindUnpackLayer` follows -
// one large sequential read is what a shared mount is good at.
//
// Empty where there is nothing to stage, which is an unpinned helper: the guest
// then has no route to it and says so, rather than this side inventing one.
func (e *Executor) stageHelper(
	ctx context.Context, m ir.Mount,
) (at string, clean func(), err error) {
	clean = func() {}

	if m.HelperID == "" || e.Mounts == "" {
		return "", clean, nil
	}

	id, err := ir.ParseNodeID(m.HelperID)
	if err != nil {
		return "", clean, nil //nolint:nilerr // an unreadable pin is an unshared cache
	}

	store, err := blob.New(filepath.Dir(e.Mounts))
	if err != nil {
		return "", clean, nil //nolint:nilerr // no store here is no helper to stage
	}

	body, err := store.Get(id)
	if err != nil {
		return "", clean, nil //nolint:nilerr // the module is not here to send
	}

	f, err := os.CreateTemp("", "earth-helper-*.wasm")
	if err != nil {
		return "", clean, fmt.Errorf("stage the helper: %w", err)
	}

	tmp := f.Name()
	clean = func() { _ = os.Remove(tmp) }

	if _, err := f.Write(body); err != nil {
		_ = f.Close()

		return "", clean, fmt.Errorf("stage the helper: %w", err)
	}

	if err := f.Close(); err != nil {
		return "", clean, fmt.Errorf("stage the helper: %w", err)
	}

	at, err = placeBlob(ctx, e.sb, tmp)
	if err != nil {
		return "", clean, fmt.Errorf("hand the helper to the guest: %w", err)
	}

	return at, clean, nil
}

// cacheMountOf is a mount as a cache request carries it.
//
// The declaration and nothing else. A cache request is about one directory, and
// what has to cross whole is what decides whether two machines are describing
// the same cache: the id, the scope, and which helper reads it (E433).
func cacheMountOf(m ir.Mount, scope string) guest.Mount {
	return guest.Mount{
		ID: m.ID, Target: m.Target, Scope: scope,
		Helper: m.Helper, HelperID: m.HelperID,
	}
}
