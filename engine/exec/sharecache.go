package exec

import (
	"context"
	"path/filepath"

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

	for _, s := range shareable(n.Op.Mounts, e.Mounts, e.Domain) {
		_ = e.Share(ctx, s.mount, s.dir)
	}
}
