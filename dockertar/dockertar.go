// Package dockertar handles the extraction and parsing of Docker image tarballs to retrieve image metadata and IDs.
package dockertar

import (
	"archive/tar"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// GetID returns the docker sha256 ID of the image stored within the given .tar file.
func GetID(tarFilePath string) (string, error) {
	tarFile, err := os.Open(tarFilePath) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("open file %s for reading: %w", tarFilePath, err)
	}
	defer tarFile.Close()

	bufTarFile := bufio.NewReader(tarFile)

	tarR := tar.NewReader(bufTarFile)
	for {
		header, err := tarR.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return "", fmt.Errorf("reading tar %s: %w", tarFilePath, err)
		}

		if header.Name == "manifest.json" && !header.FileInfo().IsDir() {
			dt, err := io.ReadAll(tarR)
			if err != nil {
				return "", fmt.Errorf("read manifest.json from tar %s: %w", tarFilePath, err)
			}

			var jsonData []struct {
				Config string `json:"Config"`
			}

			err = json.Unmarshal(dt, &jsonData)
			if err != nil {
				return "", fmt.Errorf("unmarshal json tar manifest for %s: %w", tarFilePath, err)
			}

			if len(jsonData) != 1 {
				return "", fmt.Errorf("unexpected len != 1 docker manifest in %s", tarFilePath)
			}

			return strings.TrimSuffix(jsonData[0].Config, ".json"), nil
		}
	}

	return "", fmt.Errorf("docker tar manifest.json not found in tar %s", tarFilePath)
}
