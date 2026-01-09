package gatewaycrafter

import (
	"encoding/json"
	"strconv"

	"github.com/EarthBuild/earthbuild/states/image"
	"github.com/EarthBuild/earthbuild/util/stringutil"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

// NewGatewayCrafter creates a new GatewayCrafter designed to be used to populate ref
// and metadata entries for the buildkit Export function.
func NewGatewayCrafter() *GatewayCrafter {
	return &GatewayCrafter{
		res: gwclient.NewResult(),
	}
}

// GatewayCrafter wraps the gwclient.Result object with a helper function which is used
// to deduplicate code between builder.go and wait_block.go eventually all SAVE IMAGE
// (and other EarthBuild exporter) logic will be triggered via the WAIT/END PopWaitBlock()
// function and code that direct accesses to the underlying result instance will be removed.
type GatewayCrafter struct {
	done bool
	res  *gwclient.Result
}

// AddPushImageEntry adds ref and metadata required to cause an image to be pushed.
func (gc *GatewayCrafter) AddPushImageEntry(
	ref gwclient.Reference,
	refID int,
	imageName string,
	shouldPush, insecurePush bool,
	imageConfig *image.Image,
	platformStr []byte,
) (string, error) {
	config, err := json.Marshal(imageConfig)
	if err != nil {
		return "", errors.Wrapf(err, "marshal save image config")
	}

	refKey := "image-" + strconv.Itoa(refID)
	refPrefix := "ref/" + refKey

	gc.AddRef(refKey, ref)

	gc.AddMeta(refPrefix+"/image.name", []byte(imageName))
	if shouldPush {
		gc.AddMeta(refPrefix+"/export-image-push", []byte("true"))
		if insecurePush {
			gc.AddMeta(refPrefix+"/insecure-push", []byte("true"))
		}
	}
	gc.AddMeta(refPrefix+"/"+exptypes.ExporterImageConfigKey, config)

	if platformStr != nil {
		gc.AddMeta(refPrefix+"/platform", platformStr)
	}

	// TODO once all earthlyoutput-metadata-related code is moved into saveimageutil, change to "return err" only
	return refPrefix, nil
}

// AddSaveArtifactLocal adds ref and metadata required to trigger an artifact export to the local host.
func (gc *GatewayCrafter) AddSaveArtifactLocal(
	ref gwclient.Reference, refID int, artifact, srcPath, destPath string,
) (string, error) {
	refKey := "dir-" + strconv.Itoa(refID)
	refPrefix := "ref/" + refKey
	gc.AddRef(refKey, ref)

	dirID := stringutil.RandomAlphanumeric(32)
	gc.AddMeta(refPrefix+"/artifact", []byte(artifact))
	gc.AddMeta(refPrefix+"/src-path", []byte(srcPath))
	gc.AddMeta(refPrefix+"/dest-path", []byte(destPath))
	gc.AddMeta(refPrefix+"/export-dir", []byte("true"))
	gc.AddMeta(refPrefix+"/dir-id", []byte(dirID))
	return dirID, nil
}

// AddRef adds a reference to the results to be exported.
// NOTE: The use of this function (outside of gatewaycrafter.go) is deprecated. This function will become private once
// all SAVE IMAGE logic is triggered via the WAIT/END PopWaitBlock() function.
func (gc *GatewayCrafter) AddRef(k string, ref gwclient.Reference) {
	gc.assertNotDone()
	if ref != nil {
		gc.res.AddRef(k, ref)
	}
}

// AddMeta adds metadata to the results to be exported.
// NOTE: The use of this function (outside of gatewaycrafter.go) is deprecated. This function will become private once
// all SAVE IMAGE logic is triggered via the WAIT/END PopWaitBlock() function.
func (gc *GatewayCrafter) AddMeta(k string, v []byte) {
	gc.assertNotDone()
	gc.res.AddMeta(k, v)
}

// GetRefsAndMetadata fetches the result Refs and Metadata; it is not reentrant.
func (gc *GatewayCrafter) GetRefsAndMetadata() (map[string]gwclient.Reference, map[string][]byte) {
	gc.assertNotDone()
	gc.done = true
	return gc.res.Refs, gc.res.Metadata
}

// GetResult fetches the gwclient result object; it is not reentrant.
func (gc *GatewayCrafter) GetResult() *gwclient.Result {
	gc.assertNotDone()
	gc.done = true
	return gc.res
}

func (gc *GatewayCrafter) assertNotDone() {
	if gc.done {
		panic("GatewayCrafter can no longer be used after a call to GetResults/GetRefsAndMetadata")
	}
}
