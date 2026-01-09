package states

import (
	"context"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/variables"
)

// VisitedCollection represents a collection of visited targets.
type VisitedCollection interface {
	// All returns all visited items.
	All() []*SingleTarget
	// Add adds a target to the collection, if it hasn't yet been visited. The returned sts is
	// either the previously visited one or a brand new one.
	Add(
		ctx context.Context,
		target domain.Target,
		platr *platutil.Resolver,
		allowPrivileged bool,
		overridingVars *variables.Scope,
		parentDepSub chan string,
	) (*SingleTarget, bool, error)
}
