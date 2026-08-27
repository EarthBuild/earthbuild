package engine

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

func TestUnmarshalSingleOrSlice(t *testing.T) {
	t.Parallel()

	type sample struct {
		Name string `json:"name"`
		Val  int    `json:"val"`
	}

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()

		res, err := unmarshalSingleOrSlice[sample]("   ")
		require.NoError(t, err)
		assert.Nil(t, res)
	})

	t.Run("single object", func(t *testing.T) {
		t.Parallel()

		res, err := unmarshalSingleOrSlice[sample](`{"name": "foo", "val": 42}`)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "foo", res[0].Name)
		assert.Equal(t, 42, res[0].Val)
	})

	t.Run("array of objects", func(t *testing.T) {
		t.Parallel()

		res, err := unmarshalSingleOrSlice[sample](`[{"name": "a", "val": 1}, {"name": "b", "val": 2}]`)
		require.NoError(t, err)
		require.Len(t, res, 2)
		assert.Equal(t, "a", res[0].Name)
		assert.Equal(t, "b", res[1].Name)
	})

	t.Run("invalid json object", func(t *testing.T) {
		t.Parallel()

		_, err := unmarshalSingleOrSlice[sample](`{invalid}`)
		require.Error(t, err)
	})

	t.Run("invalid json array", func(t *testing.T) {
		t.Parallel()

		_, err := unmarshalSingleOrSlice[sample](`[invalid]`)
		require.Error(t, err)
	})
}

//nolint:goconst
func TestBuildAppleMountArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mounts   []Mount
		expected []string
	}{
		{
			name:     "empty mounts",
			mounts:   nil,
			expected: []string{},
		},
		{
			name: "volume mount",
			mounts: []Mount{
				{
					Type:     MountVolume,
					Source:   "earthly-cache",
					Dest:     "/tmp/earthbuild",
					ReadOnly: false,
				},
			},
			expected: []string{
				"--mount", "type=volume,source=earthly-cache,target=/tmp/earthbuild",
			},
		},
		{
			name: "bind mount read-write",
			mounts: []Mount{
				{
					Type:     MountBind,
					Source:   "/sys/fs/cgroup",
					Dest:     "/sys/fs/cgroup",
					ReadOnly: false,
				},
			},
			expected: []string{
				"--mount", "type=bind,source=/sys/fs/cgroup,target=/sys/fs/cgroup",
			},
		},
		{
			name: "bind mount read-only",
			mounts: []Mount{
				{
					Type:     MountBind,
					Source:   "/Users/test/.earthly/certs",
					Dest:     "/etc/earth-certs",
					ReadOnly: true,
				},
			},
			expected: []string{
				"--mount", "type=bind,source=/Users/test/.earthly/certs,target=/etc/earth-certs,readonly",
			},
		},
		{
			name: "multiple mixed mounts",
			mounts: []Mount{
				{
					Type:     MountVolume,
					Source:   "my-vol",
					Dest:     "/data",
					ReadOnly: false,
				},
				{
					Type:     MountBind,
					Source:   "/host/certs",
					Dest:     "/etc/earth-certs",
					ReadOnly: true,
				},
			},
			expected: []string{
				"--mount", "type=volume,source=my-vol,target=/data",
				"--mount", "type=bind,source=/host/certs,target=/etc/earth-certs,readonly",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := buildAppleMountArgs(tt.mounts)
			assert.Equal(t, tt.expected, args)
		})
	}
}
