package earthfile2llb

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportRecordingGwClient extends fakeGwClient with the two extra gateway calls
// that waitBlock.saveImages makes, and records every image name it exports.
type exportRecordingGwClient struct {
	fakeGwClient

	exported []string
	pushed   []string
	mu       sync.Mutex
}

func (f *exportRecordingGwClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{SessionID: "test-session"}
}

func (f *exportRecordingGwClient) Export(_ context.Context, req gwclient.ExportRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for key, val := range req.Metadata {
		refPrefix, ok := strings.CutSuffix(key, "/image.name")
		if !ok {
			continue
		}

		f.exported = append(f.exported, string(val))

		if string(req.Metadata[refPrefix+"/export-image-push"]) == "true" {
			f.pushed = append(f.pushed, string(val))
		}
	}

	return nil
}

// A BUILD inside an explicit WAIT ... END must have its image exported (and
// pushed, under --push) by the time END returns. That ordering is the whole
// point of WAIT/END: a later RUN --push can rely on the image being in the
// registry.
//
// The child converter's waitBlockStack starts as [parent's WAIT block], so
// len(waitBlockStack) == 1 and, with --use-inline-cache and no
// --global-wait-end, SaveImage leaves SkipBuilder == false. saveImages now
// delegates every SkipBuilder == false item to builder.go, which only runs after
// the whole build has been converted, and waitStates skips the child's main
// state because isStateExportedUnlocked ignores SkipBuilder. So END returns
// without the image having been built, exported or pushed.
func TestWaitEndExportsBuildChildImageUnderInlineCache(t *testing.T) {
	t.Parallel()

	const tag = "registry.example.com/myimg:latest"

	gw := &exportRecordingGwClient{}

	// parentWait is the block the parent opened with WAIT; the child is
	// converted through BUILD inside it.
	parentWait := newWaitBlock()

	child, eg := newFinalizeTestConverter(t, finalizeTestOpt{
		gw:          gw,
		export:      ExportAll,
		doPushes:    true,
		waitBlock:   parentWait,
		waitBlockOn: true,
	})
	child.opt.UseInlineCache = true
	child.mts.Final.RanFromLike = true
	child.mts.Final.MainState = pllb.Image("alpine:3.20")

	// child: SAVE IMAGE --push registry.example.com/myimg:latest
	err := child.SaveImage(t.Context(), []string{tag}, true, false, false, nil, false)
	require.NoError(t, err)

	// FinalizeStates adds the child's main state to the parent's WAIT block and
	// turns its pushes on. Its own async force-execution is drained here so that
	// only solves made by END are counted below.
	_, err = child.FinalizeStates(t.Context())
	require.NoError(t, err)
	require.NoError(t, eg.Wait())

	solvesBeforeEnd := gw.solves.Load()

	// parent: END
	err = parentWait.Wait(t.Context(), true, true)
	require.NoError(t, err)

	assert.Greater(t, gw.solves.Load(), solvesBeforeEnd,
		"END must solve the child's image (or at least its main state)")
	assert.Contains(t, gw.exported, tag, "END must export the child's image")
	assert.Contains(t, gw.pushed, tag, "END must push the child's SAVE IMAGE --push image")
}
