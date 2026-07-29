// Package semconv defines OpenTelemetry semantic conventions for earth's telemetry data.
package semconv

import "go.opentelemetry.io/otel/attribute"

const (
	// FileCopyMethod is the name of the attribute that represents the method used to copy a file.
	FileCopyMethod = attribute.Key("earth.file.copy_method")

	// ArtifactLocalDestinations is the name of the attribute that represents the local destinations on the host
	// machine where artifacts are saved.
	ArtifactLocalDestinations = attribute.Key("earth.artifact.local_destinations")

	// ProcessRole is the name of the attribute that represents which of earth's processes emitted the telemetry.
	ProcessRole = attribute.Key("earth.process.role")

	// ProcessNesting is the name of the attribute that distinguishes an outer build from one running inside a
	// WITH DOCKER build (earth-in-earth), whose processes are otherwise indistinguishable.
	ProcessNesting = attribute.Key("earth.process.nesting")

	// Target is the name of the attribute that represents the target being built, e.g. "+build".
	Target = attribute.Key("earth.target")

	// BuildkitContainerName is the name of the attribute that represents the container buildkitd runs in.
	BuildkitContainerName = attribute.Key("earth.buildkit.container.name")

	// InstallationName is the name of the attribute that represents the installation the build belongs to;
	// installations have their own buildkitd container and cache.
	InstallationName = attribute.Key("earth.installation.name")
)

var (
	// FileCopyMethodCopyOnWrite is the value of the FileCopyMethod attribute when copy-on-write was used to copy a file.
	FileCopyMethodCopyOnWrite = FileCopyMethod.String("copy-on-write")
	// FileCopyMethodHardlink is the value of the FileCopyMethod attribute when a hardlink was used to copy a file.
	FileCopyMethodHardlink = FileCopyMethod.String("hardlink")
	// FileCopyMethodCopy is the value of the FileCopyMethod attribute when a full copy was used to copy a file.
	FileCopyMethodCopy = FileCopyMethod.String("copy")

	// ProcessRoleCLI is the value of the ProcessRole attribute for the earth CLI process.
	ProcessRoleCLI = ProcessRole.String("cli")
	// ProcessRoleBuildkitd is the value of the ProcessRole attribute for the buildkitd daemon.
	ProcessRoleBuildkitd = ProcessRole.String("buildkitd")

	// ProcessNestingInner is the value of the ProcessNesting attribute for a build running inside WITH DOCKER.
	ProcessNestingInner = ProcessNesting.String("inner")
	// ProcessNestingOuter is the value of the ProcessNesting attribute for a build running on the host.
	ProcessNestingOuter = ProcessNesting.String("outer")
)
