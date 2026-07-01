package containerutil

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppleContainerInspectUnmarshal(t *testing.T) {
	t.Parallel()

	data := `[
		{
			"status": {
				"state": "running",
				"networks": [
					{"address": "192.168.64.2"}
				]
			},
			"configuration": {
				"id": "test-container-id",
				"image": {
					"reference": "docker.io/library/ubuntu:latest"
				},
				"labels": {
					"key": "value"
				}
			}
		}
	]`

	var inspects []appleContainerInspect

	err := json.Unmarshal([]byte(data), &inspects)
	require.NoError(t, err)
	require.Len(t, inspects, 1)

	c := inspects[0]
	assert.Equal(t, "running", c.Status.State)
	require.Len(t, c.Status.Networks, 1)
	assert.Equal(t, "192.168.64.2", c.Status.Networks[0].Address)
	assert.Equal(t, "test-container-id", c.Configuration.ID)
	assert.Equal(t, "docker.io/library/ubuntu:latest", c.Configuration.Image.Reference)
	assert.Equal(t, "value", c.Configuration.Labels["key"])
}

func TestAppleImageInspectUnmarshal(t *testing.T) {
	t.Parallel()

	data := `[
		{
			"id": "sha256:abcd1234",
			"configuration": {
				"name": "docker.io/library/ubuntu:latest"
			},
			"variants": [
				{
					"platform": {
						"os": "linux",
						"architecture": "arm64"
					}
				}
			]
		}
	]`

	var inspects []appleImageInspect

	err := json.Unmarshal([]byte(data), &inspects)
	require.NoError(t, err)
	require.Len(t, inspects, 1)

	img := inspects[0]
	assert.Equal(t, "docker.io/library/ubuntu:latest", img.Configuration.Name)
	assert.Equal(t, "sha256:abcd1234", img.ID)
	require.Len(t, img.Variants, 1)
	assert.Equal(t, "linux", img.Variants[0].Platform.OS)
	assert.Equal(t, "arm64", img.Variants[0].Platform.Architecture)
}

func TestAppleVolumeInspectUnmarshal(t *testing.T) {
	t.Parallel()

	data := `[
		{
			"id": "test-volume",
			"configuration": {
				"name": "test-volume",
				"source": "/var/lib/container/volumes/test-volume",
				"sizeInBytes": 1048576
			}
		}
	]`

	var inspects []appleVolumeInspect

	err := json.Unmarshal([]byte(data), &inspects)
	require.NoError(t, err)
	require.Len(t, inspects, 1)

	v := inspects[0]
	assert.Equal(t, "test-volume", v.Configuration.Name)
	assert.Equal(t, "/var/lib/container/volumes/test-volume", v.Configuration.Source)
	assert.Equal(t, uint64(1048576), v.Configuration.SizeInBytes)
}
