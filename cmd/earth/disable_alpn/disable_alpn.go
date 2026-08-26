// Package disable_alpn sets GRPC_ENFORCE_ALPN_ENABLED environment variable to false during initialization.
package disable_alpn

import "os"

func init() {
	_ = os.Setenv("GRPC_ENFORCE_ALPN_ENABLED", "false")
}
