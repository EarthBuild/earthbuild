package exec

import (
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

func writeFileForTest(store string, key ir.NodeID, body string) error {
	return os.WriteFile(filepath.Join(store, "map", key.String()), []byte(body), 0o600)
}
