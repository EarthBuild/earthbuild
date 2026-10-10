package earthfile2llb

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/util/gatewaycrafter"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// metaTrue is how a gateway export request's metadata says yes.
const metaTrue = "true"

// exportedImage is one image entry of a gateway export request.
type exportedImage struct {
	name     string
	platform string
	// manifestKey is the export coordinator key of a multi-platform image's
	// local, per-platform copy: export-image-local-registry or
	// export-image-manifest-key.
	manifestKey string
	push        bool
	local       bool
}

// exportRequest is what one gateway Export was asked to do.
type exportRequest struct {
	images []exportedImage
	// waitFor is the export-image-wait-for list of the request's local
	// multi-platform images, if any.
	waitFor []string
}

// requestRecordingGwClient is a gateway client that records every Export
// request it is given, as the images it exports and pushes. If hold is set,
// every Export whose request hold returns true for first sends on held (if not
// nil), then waits until release is closed or its context is done.
type requestRecordingGwClient struct {
	fakeGwClient

	hold     func(exportRequest) bool
	held     chan<- struct{}
	release  <-chan struct{}
	requests []exportRequest
	mu       sync.Mutex
}

func (f *requestRecordingGwClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{SessionID: "test-session"}
}

func (f *requestRecordingGwClient) Export(ctx context.Context, req gwclient.ExportRequest) error {
	var got exportRequest

	for key, val := range req.Metadata {
		refPrefix, ok := strings.CutSuffix(key, "/image.name")
		if !ok {
			continue
		}

		img := exportedImage{
			name:     string(val),
			platform: string(req.Metadata[refPrefix+"/platform"]),
			push:     string(req.Metadata[refPrefix+"/export-image-push"]) == metaTrue,
		}

		if key, ok := req.Metadata[refPrefix+"/export-image-local-registry"]; ok {
			img.local = true
			img.manifestKey = string(key)
		}

		if string(req.Metadata[refPrefix+"/export-image"]) == metaTrue {
			img.local = true
			img.manifestKey = string(req.Metadata[refPrefix+"/export-image-manifest-key"])
		}

		if waitFor, ok := req.Metadata[refPrefix+"/export-image-wait-for"]; ok {
			got.waitFor = strings.Fields(string(waitFor))
		}

		got.images = append(got.images, img)
	}

	f.mu.Lock()
	f.requests = append(f.requests, got)
	f.mu.Unlock()

	if f.hold == nil || !f.hold(got) {
		return nil
	}

	if f.held != nil {
		f.held <- struct{}{}
	}

	select {
	case <-f.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// recorded returns the export requests made so far.
func (f *requestRecordingGwClient) recorded() []exportRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.requests)
}

// pushedPlatforms returns the platforms req pushes under name: the manifest
// list the registry ends up with for that tag.
func (req exportRequest) pushedPlatforms(name string) []string {
	var platforms []string

	for _, img := range req.images {
		if img.name == name && img.push {
			platforms = append(platforms, img.platform)
		}
	}

	slices.Sort(platforms)

	return platforms
}

// localNames returns the names req exports to the local container engine.
func (req exportRequest) localNames() []string {
	var names []string

	for _, img := range req.images {
		if img.local {
			names = append(names, img.name)
		}
	}

	slices.Sort(names)

	return names
}

// manifestKeys returns the export coordinator keys builder.go is given once req
// is exported, to load name's per-platform images under: the wait-for list of a
// tar export, or the images pulled from the local registry.
func (req exportRequest) manifestKeys() []string {
	if len(req.waitFor) != 0 {
		return req.waitFor
	}

	var keys []string

	for _, img := range req.images {
		if img.local && img.manifestKey != "" {
			keys = append(keys, img.manifestKey)
		}
	}

	return keys
}

// newPlatformImageConverter converts a target for one platform, in wb, as BUILD
// --platform does, and runs SAVE IMAGE (--push, if push) on it.
func newPlatformImageConverter(
	t *testing.T, gw *requestRecordingGwClient, coordinator *gatewaycrafter.ExportCoordinator,
	wb *waitBlock, platform string, export Export, push bool,
) *Converter {
	t.Helper()

	c, eg := newFinalizeTestConverter(t, finalizeTestOpt{
		gw:          gw,
		export:      export,
		doPushes:    push,
		waitBlock:   wb,
		waitBlockOn: true,
	})
	c.opt.ExportCoordinator = coordinator

	p, err := c.platr.Parse(platform)
	require.NoError(t, err)

	c.platr = c.platr.SubResolver(p)
	saveTestImage(push)(t, c)

	_, err = c.FinalizeStates(t.Context())
	require.NoError(t, err)
	require.NoError(t, eg.Wait())

	return c
}

// A WAIT block exports a multi-platform tag as one manifest list: whatever it
// pushes under that tag replaces what the registry had. So when an image is
// pushed for one platform in a first WAIT ... END, and a second WAIT ... END
// BUILDs it for that platform and another one, the second push must still
// carry both platforms. The first platform's target is converted once; the
// second BUILD only re-attaches its wait items (AttachTopLevelWaitItems).
//
// Only the platform that is new may be solved and exported again; the platform
// pushed already is reused, but it stays in the manifest list.
//
// The same goes for a local export: the per-platform image the first WAIT
// loaded (myimg:latest_linux_amd64) is not loaded again, but the local
// multi-platform image is still made from both platforms.
func TestLaterMultiPlatformExportKeepsEarlierPlatforms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		export           Export
		push             bool
		useLocalRegistry bool
	}{
		{name: "push", export: ExportNone, push: true},
		{name: "local export", export: ExportAll},
		{name: "local export through the local registry", export: ExportAll, useLocalRegistry: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gw := &requestRecordingGwClient{}
			coordinator := gatewaycrafter.NewExportCoordinator()

			// WAIT
			//   BUILD --platform=linux/amd64 +img
			// END
			first := newWaitBlock()
			amd64 := newPlatformImageConverter(t, gw, coordinator, first, "linux/amd64", tt.export, tt.push)
			amd64.opt.UseLocalRegistry = tt.useLocalRegistry

			require.NoError(t, first.Wait(t.Context(), tt.push, amd64.opt.doSaves()))

			// WAIT
			//   BUILD --platform=linux/amd64 --platform=linux/arm64 +img
			// END
			second := newWaitBlock()

			if tt.push {
				amd64.mts.Final.SetDoPushes()
			}

			amd64.mts.Final.AttachTopLevelWaitItems(t.Context(), second)

			arm64 := newPlatformImageConverter(t, gw, coordinator, second, "linux/arm64", tt.export, tt.push)
			arm64.opt.UseLocalRegistry = tt.useLocalRegistry

			require.NoError(t, second.Wait(t.Context(), tt.push, arm64.opt.doSaves()))

			requests := gw.recorded()
			require.Len(t, requests, 2, "each WAIT ... END exports once")

			wantPlatforms := []string{"linux/amd64", "linux/arm64"}

			if tt.push {
				assert.Equal(t, []string{"linux/amd64"}, requests[0].pushedPlatforms(scenarioTag))
				assert.Equal(t, wantPlatforms, requests[1].pushedPlatforms(scenarioTag),
					"the second push of %s must not drop the platform the first one pushed", scenarioTag)

				return
			}

			assert.Equal(t, []string{scenarioTag + "_linux_arm64"}, requests[1].localNames(),
				"the second WAIT must only load the platform that is new")

			lists, err := coordinator.ManifestLists(requests[1].manifestKeys())
			require.NoError(t, err)

			var platforms []string
			for _, m := range lists[scenarioTag] {
				platforms = append(platforms, m.Platform.String())
			}

			slices.Sort(platforms)

			assert.Equal(t, wantPlatforms, platforms,
				"the local %s must still be made from both platforms", scenarioTag)
		})
	}
}
