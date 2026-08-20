//go:build !linux

package guest

import "github.com/EarthBuild/earthbuild/engine/layer"

// ownIDMaps is the identity off Linux: no user namespaces, so what this process
// sees is what the store holds.
func OwnIDMaps() (uids, gids layer.IDMap) { return layer.IDMap{}, layer.IDMap{} }
