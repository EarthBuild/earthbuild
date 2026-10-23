package earthfile2llb

import (
	"context"
	"sync"
	"testing"

	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type mockImageMetaResolver struct {
	lastOpt sourceresolver.Opt
	mu      sync.Mutex
	calls   int
}

func (m *mockImageMetaResolver) ResolveImageConfig(
	_ context.Context, ref string, opt sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++
	m.lastOpt = opt

	mode := ""
	if opt.ImageOpt != nil {
		mode = opt.ImageOpt.ResolveMode
	}

	return ref, digest.FromString(ref + ":" + mode), []byte("{}"), nil
}

func (m *mockImageMetaResolver) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.calls
}

func TestCachedMetaResolver_ResolveModeAffectsCacheKey(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	mock := &mockImageMetaResolver{}
	cmr := NewCachedMetaResolver(mock)

	ref := "alpine:latest"

	optDefault := sourceresolver.Opt{
		ImageOpt: &sourceresolver.ResolveImageOpt{
			ResolveMode: "",
		},
	}

	ref1, dgst1, _, err := cmr.ResolveImageConfig(ctx, ref, optDefault)
	require.NoError(t, err)
	require.Equal(t, ref, ref1)
	require.Equal(t, 1, mock.callCount())

	// Repeated resolution with empty ResolveMode should hit the cache.
	_, dgst2, _, err := cmr.ResolveImageConfig(ctx, ref, optDefault)
	require.NoError(t, err)
	require.Equal(t, dgst1, dgst2)
	require.Equal(t, 1, mock.callCount())

	// Resolution with "force-pull" must trigger a new cache key and resolution.
	optForcePull := sourceresolver.Opt{
		ImageOpt: &sourceresolver.ResolveImageOpt{
			ResolveMode: "force-pull",
		},
	}

	ref3, dgst3, _, err := cmr.ResolveImageConfig(ctx, ref, optForcePull)
	require.NoError(t, err)
	require.Equal(t, ref, ref3)
	require.NotEqual(t, dgst1, dgst3)
	require.Equal(t, 2, mock.callCount())

	// Repeated resolution with "force-pull" should hit the new cache entry.
	_, dgst4, _, err := cmr.ResolveImageConfig(ctx, ref, optForcePull)
	require.NoError(t, err)
	require.Equal(t, dgst3, dgst4)
	require.Equal(t, 2, mock.callCount())
}

func TestCachedMetaResolver_ReturnsClonedConfig(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	mock := &mockImageMetaResolver{}
	cmr := NewCachedMetaResolver(mock)

	ref := "alpine:latest"
	opt := sourceresolver.Opt{}

	_, _, cfg1, err := cmr.ResolveImageConfig(ctx, ref, opt)
	require.NoError(t, err)
	require.Equal(t, []byte("{}"), cfg1)

	// Mutate returned slice
	cfg1[0] = 'X'

	// Second fetch should return unmodified cached copy
	_, _, cfg2, err := cmr.ResolveImageConfig(ctx, ref, opt)
	require.NoError(t, err)
	require.Equal(t, []byte("{}"), cfg2)
}
