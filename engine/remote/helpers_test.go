package remote_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func manifestOf(t *testing.T, files map[string]string) []byte {
	t.Helper()

	dir := t.TempDir()

	for name, body := range files {
		at := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	return m
}
