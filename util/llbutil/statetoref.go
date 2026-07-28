package llbutil

import (
	"context"
	"fmt"

	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// StateToRef takes an LLB state, solves it using gateway and returns the ref.
func StateToRef(
	ctx context.Context,
	gwClient gwclient.Client,
	state pllb.State,
	noCache bool,
	platr *platutil.Resolver,
	cacheImports []string,
) (gwclient.Reference, error) {
	platform := platr.SubPlatform(platr.Current())

	if noCache {
		state = state.SetMarshalDefaults(llb.IgnoreCache)
	}

	coes := make([]gwclient.CacheOptionsEntry, 0, len(cacheImports))
	for _, ci := range cacheImports {
		coe := gwclient.CacheOptionsEntry{
			Type:  "registry",
			Attrs: map[string]string{"ref": ci},
		}
		coes = append(coes, coe)
	}

	def, err := state.Marshal(ctx, llb.Platform(platr.ToLLBPlatform(platform)))
	if err != nil {
		return nil, fmt.Errorf("marshal state: %w", err)
	}

	r, err := gwClient.Solve(ctx, gwclient.SolveRequest{
		Definition:   def.ToPB(),
		CacheImports: coes,
	})
	if err != nil {
		return nil, fmt.Errorf("solve state: %w", err)
	}

	ref, err := r.SingleRef()
	if err != nil {
		return nil, fmt.Errorf("single ref: %w", err)
	}

	return ref, nil
}
