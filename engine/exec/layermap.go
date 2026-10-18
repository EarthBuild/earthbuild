package exec

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The map from a derivation to the layer it produced.
//
// This is 𝔄 in miniature (green paper §2.3): a key names a derivation and what
// it maps to is the digest of the result. The layer itself is filed under its
// contents (§3.2), so something has to know the two are related - and that
// something must be a map rather than a directory name, which is the whole of
// what E507 was about.
//
// A cache of a pure function. A lost or corrupt record costs a recomputation and
// can never yield a wrong layer, provided unreadable is reported as *absent*:
// a zero id read from a truncated file names the empty layer, and names it with
// confidence.
func layerMapDir(store string) string { return filepath.Join(store, "map") }

// layerNamed is the layer a derivation produced here before, if it did.
func layerNamed(store string, key ir.NodeID) (ir.NodeID, bool) {
	b, err := os.ReadFile(filepath.Join(layerMapDir(store), key.String()))
	if err != nil {
		return ir.NodeID{}, false
	}

	id, err := ir.ParseNodeID(strings.TrimSpace(string(b)))
	if err != nil {
		return ir.NodeID{}, false
	}

	return id, true
}

// rememberLayer records what a derivation produced. Best-effort: losing it costs
// one capture, so a failure here is not worth failing a build over.
func rememberLayer(store string, key, id ir.NodeID) {
	if os.MkdirAll(layerMapDir(store), 0o750) != nil {
		return
	}

	_ = os.WriteFile(filepath.Join(layerMapDir(store), key.String()), []byte(id.String()), 0o600)
}
