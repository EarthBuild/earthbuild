package fleet

import (
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// WithNodes makes a store answer for the content-addressed nodes beside it.
//
// **The driver's half of `fleet.Nodes`, which was missing.** A worker serves its
// store's `nodes/` and a driver served only layers - and the driver is the
// machine holding the helper module every worker has to run and the units of
// every cache it has filled. A worker asking for one got "no peer served it"
// from the one peer that certainly had it.
//
// `root` is the store directory. Empty leaves the store exactly as it was, which
// is what a store living somewhere this process cannot read wants: on a VM
// backend `nodes/` is on the guest's device, and a `Nodes` rooted at the host's
// path would claim nothing and serve nothing, honestly.
func WithNodes(s Store, root string) Store {
	if root == "" {
		return s
	}

	return &alsoNodes{Store: s, nodes: &Nodes{Root: root}}
}

// alsoNodes is a store plus the nodes beside it.
//
// Embedded rather than reimplemented, so that a store's `Put` - and anything
// else it grows - keeps working through the wrapper. Only the two questions a
// node can answer are intercepted.
type alsoNodes struct {
	Store

	nodes *Nodes
}

// Has is either place, layers first.
//
// Layers first because that is what most ids are and what this answered for
// before nodes existed; a collision between the two is a hash collision and not
// an ordering question. The same argument `Parts.Has` makes, said where a driver
// can hear it.
func (a *alsoNodes) Has(id ir.NodeID) bool {
	return a.Store.Has(id) || a.nodes.Has(id)
}

// Get is whichever place claimed it.
func (a *alsoNodes) Get(id ir.NodeID) ([]byte, error) {
	if a.Store.Has(id) {
		return a.Store.Get(id) //nolint:wrapcheck // the store's own error
	}

	return a.nodes.Get(id) //nolint:wrapcheck // the node store's own error
}
