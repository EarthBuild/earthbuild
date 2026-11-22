package guest

import "path/filepath"

// cacheSource is the directory behind a cache mount's id.
//
// **Here rather than in `mount_linux.go` so that it can be tested at all.** The
// rule is one line and one line is exactly what gets changed without anybody
// noticing; the file that used to hold it carries a build tag, so a test of it
// would run on one platform and the rule applies on every one.
//
// `Scope` is empty for a cache whose author made no claim, and `filepath.Join`
// drops empty elements - so the overwhelmingly common case resolves to the path
// it has always resolved to, and no existing cache moves.
func cacheSource(store string, m Mount) string {
	return filepath.Join(store, m.ID, m.Scope)
}
