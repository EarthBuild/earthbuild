package earthfile2llb

import (
	"context"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

type contextKey string

var contextKeySourceLocation contextKey = "sourceLocation"

// ContextWithSourceLocation returns a new context with the given source location.
func ContextWithSourceLocation(ctx context.Context, sl *earthfile.SourceLocation) context.Context {
	if sl == nil {
		return ctx
	}

	return context.WithValue(ctx, contextKeySourceLocation, sl)
}

// SourceLocationFromContext returns the source location from the given context.
func SourceLocationFromContext(ctx context.Context) *earthfile.SourceLocation {
	sl, _ := ctx.Value(contextKeySourceLocation).(*earthfile.SourceLocation)
	return sl
}
