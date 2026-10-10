//go:build !unix

package guest

import "os"

// idOf has no answer here, so every file is copied and no link is inferred.
func idOf(os.FileInfo) fileID { return fileID{} }
