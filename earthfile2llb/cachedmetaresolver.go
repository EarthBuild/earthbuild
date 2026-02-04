package earthfile2llb

import (
	"context"

	"github.com/EarthBuild/earthbuild/util/syncutil/synccache"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

var _ llb.ImageMetaResolver = &CachedMetaResolver{}

type cachedMetaResolverKey struct {
	ref      string
	platform string
}

type cachedMetaResolverEntry struct {
	ref    string
	dgst   digest.Digest
	config []byte
}

// CachedMetaResolver is an image meta resolver with a local cache.
type CachedMetaResolver struct {
	metaResolver llb.ImageMetaResolver
	cache        *synccache.SyncCache // cachedMetaResolverKey -> cachedMetaResolverEntry
}

// NewCachedMetaResolver creates a new cached meta resolver based on an underlying meta resolver
// which needs to be provided.
func NewCachedMetaResolver(metaResolver llb.ImageMetaResolver) *CachedMetaResolver {
	return &CachedMetaResolver{
		metaResolver: metaResolver,
		cache:        synccache.New(),
	}
}

// ResolveImageConfig implements llb.ImageMetaResolver.ResolveImageConfig.
func (cmr *CachedMetaResolver) ResolveImageConfig(
	ctx context.Context, ref string, opt llb.ResolveImageConfigOpt,
) (string, digest.Digest, []byte, error) {
	platformStr := ""
	if opt.Platform != nil {
		platformStr = platforms.Format(*opt.Platform)
	}

	key := cachedMetaResolverKey{
		ref:      ref,
		platform: platformStr,
	}

	value, err := cmr.cache.Do(ctx, key, func(ctx context.Context, _ any) (any, error) {
		reference, dgst, config, err := cmr.metaResolver.ResolveImageConfig(ctx, ref, opt)
		if err != nil {
			return nil, err
		}

		return cachedMetaResolverEntry{
			ref:    reference,
			dgst:   dgst,
			config: config,
		}, nil
	})
	if err != nil {
		return "", "", nil, err
	}

	entry, ok := value.(cachedMetaResolverEntry)
	if !ok {
		return "", "", nil, errors.Errorf("want cachedMetaResolverEntry, got %T", value)
	}

	return entry.ref, entry.dgst, entry.config, nil
}
