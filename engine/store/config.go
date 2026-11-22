package store

import (
	"encoding/json"
	"fmt"
	"os"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ConfigSuffix names the file holding what an image declared, beside its layer.
const ConfigSuffix = ".config.json"

// ReadImageConfig reads what an image declared, from beside its layer.
func ReadImageConfig(path string) (ocispec.ImageConfig, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a path this engine derived
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("read an image configuration: %w", err)
	}

	var cfg ocispec.ImageConfig

	err = json.Unmarshal(b, &cfg)
	if err != nil {
		return ocispec.ImageConfig{}, fmt.Errorf("parse the image configuration at %s: %w", path, err)
	}

	return cfg, nil
}
