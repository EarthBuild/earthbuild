// Package semconv defines OpenTelemetry semantic conventions for earth's telemetry data.
package semconv

import "go.opentelemetry.io/otel/attribute"

const (
	// FileCopyMethod is the name of the attribute that represents the method used to copy a file.
	FileCopyMethod = attribute.Key("earth.file.copy_method")

	// ArtifactLocalDestinations is the name of the attribute that represents the local destinations on the host
	// machine where artifacts are saved.
	ArtifactLocalDestinations = attribute.Key("earth.artifact.local_destinations")
)

var (
	// FileCopyMethodCopyOnWrite is the value of the FileCopyMethod attribute when copy-on-write was used to copy a file.
	FileCopyMethodCopyOnWrite = FileCopyMethod.String("copy-on-write")
	// FileCopyMethodHardlink is the value of the FileCopyMethod attribute when a hardlink was used to copy a file.
	FileCopyMethodHardlink = FileCopyMethod.String("hardlink")
	// FileCopyMethodCopy is the value of the FileCopyMethod attribute when a full copy was used to copy a file.
	FileCopyMethodCopy = FileCopyMethod.String("copy")
)
