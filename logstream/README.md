# logstream

This package contains Protocol Buffer definitions and generated Go code for logging and manifest tracking in EarthBuild.

## Overview

The logstream package defines the data structures used for:
- Build manifests (targets, commands, status)
- Log streaming (raw and formatted logs)
- Delta updates for incremental manifest changes

## History

This package was originally part of `github.com/earthly/cloud-api` but has been internalized into the EarthBuild project to remove the external dependency. The Protocol Buffer definitions and generated code provide the same functionality as the original cloud-api version.

## Files

- `manifest.proto` - Defines build manifest structures (RunManifest, TargetManifest, CommandManifest, etc.)
- `delta.proto` - Defines delta/incremental update structures (Delta, DeltaLog, DeltaFormattedLog, etc.)
- `manifest.pb.go` - Generated Go code from manifest.proto
- `delta.pb.go` - Generated Go code from delta.proto

## Regenerating Go Code

If you need to regenerate the Go code from the proto files:

```bash
# Install protoc and protoc-gen-go if not already installed
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

# Generate Go code
protoc --go_out=. --go_opt=paths=source_relative \
    manifest.proto delta.proto
```

Alternatively, you can use buf or other proto generation tools.

## Usage

This package is used throughout EarthBuild for:
- Tracking build state and progress
- Streaming logs from build commands
- Reporting build status and failures
- Managing build manifests

See the `logbus` package for the main consumers of these types.
