package earthfile

import (
	"time"
)

// NOTE: Any new flags must be accompanied by the introduction of a new `VERSION` feature flag.
// This applies to new features which do **not** break backwards compatibility, which is needed
// to ensure an Earthfile that uses `VERSION 0.7` can be built by **any** of the earthly-v0.7.x binaries.

// IfOpts contains options for the IF command.
type IfOpts struct {
	Secrets    []string `description:"Make available a secret"                       long:"secret"`
	Mounts     []string `description:"Mount a file or directory"                     long:"mount"`
	Privileged bool     `description:"Enable privileged mode"                        long:"privileged"`
	WithSSH    bool     `description:"Make available the SSH agent of the host"      long:"ssh"`
	NoCache    bool     `description:"Always run this specific item, ignoring cache" long:"no-cache"`
}

// ForOpts contains options for the FOR command.
type ForOpts struct {
	Separators string   `description:"The separators to use for tokenizing the output of the IN expression. Defaults to '\n\t '" long:"sep"`        //nolint:lll
	Secrets    []string `description:"Make available a secret"                                                                   long:"secret"`     //nolint:lll
	Mounts     []string `description:"Mount a file or directory"                                                                 long:"mount"`      //nolint:lll
	Privileged bool     `description:"Enable privileged mode"                                                                    long:"privileged"` //nolint:lll
	WithSSH    bool     `description:"Make available the SSH agent of the host"                                                  long:"ssh"`        //nolint:lll
	NoCache    bool     `description:"Always run this specific item, ignoring cache"                                             long:"no-cache"`   //nolint:lll
}

// RunOpts contains options for the RUN command.
type RunOpts struct {
	OIDC            string   `description:"make credentials from oidc provider (currently only works with AWS) available to RUN commands"  long:"oidc"`             //nolint:lll
	Network         string   `description:"Network to use; currently network=none is only supported"                                       long:"network"`          //nolint:lll
	Secrets         []string `description:"Make available a secret"                                                                        long:"secret"`           //nolint:lll
	Mounts          []string `description:"Mount a file or directory"                                                                      long:"mount"`            //nolint:lll
	Push            bool     `description:"Execute this command only if the build succeeds and also if earthbuild is invoked in push mode" long:"push"`             //nolint:lll
	Privileged      bool     `description:"Enable privileged mode"                                                                         long:"privileged"`       //nolint:lll
	WithEntrypoint  bool     `description:"Include the entrypoint of the image when running the command"                                   long:"entrypoint"`       //nolint:lll
	WithDocker      bool     `description:"Deprecated"                                                                                     long:"with-docker"`      //nolint:lll
	WithSSH         bool     `description:"Make available the SSH agent of the host"                                                       long:"ssh"`              //nolint:lll
	WithAWS         bool     `description:"Make any AWS credentials set in the environment available to RUN commands"                      long:"aws"`              //nolint:lll
	NoCache         bool     `description:"Always run this specific item, ignoring cache"                                                  long:"no-cache"`         //nolint:lll
	Interactive     bool     `description:"Run this command with an interactive session, without saving changes"                           long:"interactive"`      //nolint:lll
	InteractiveKeep bool     `description:"Run this command with an interactive session, saving changes"                                   long:"interactive-keep"` //nolint:lll
	RawOutput       bool     `description:"Do not prefix output with target. Print Raw"                                                    long:"raw-output"`       //nolint:lll
}

// FromOpts contains options for the FROM command.
type FromOpts struct {
	Platform        string   `description:"The platform to use"                                           long:"platform"`         //nolint:lll
	BuildArgs       []string `description:"A build arg override passed on to a referenced earth target"   long:"build-arg"`        //nolint:lll
	AllowPrivileged bool     `description:"Allow commands under remote targets to enable privileged mode" long:"allow-privileged"` //nolint:lll
	PassArgs        bool     `description:"Pass arguments to external targets"                            long:"pass-args"`        //nolint:lll
}

// FromDockerfileOpts contains options for the FROM DOCKERFILE command.
type FromDockerfileOpts struct {
	Platform        string   `description:"The platform to use"                                                                                 long:"platform"`         //nolint:lll
	Target          string   `description:"The Dockerfile target to inherit from"                                                               long:"target"`           //nolint:lll
	Path            string   `description:"The Dockerfile location on the host, relative to the current Earthfile, or as an artifact reference" short:"f"`               //nolint:lll
	BuildArgs       []string `description:"A build arg override passed on to a referenced earth target and also to the Dockerfile build"        long:"build-arg"`        //nolint:lll
	AllowPrivileged bool     `description:"Allow command to assume privileged mode"                                                             long:"allow-privileged"` //nolint:lll
}

// CopyOpts contains options for the COPY command.
type CopyOpts struct {
	From            string   `description:"Not supported"                                                           long:"from"`              //nolint:lll
	Chown           string   `description:"Apply a specific group and/or owner to the copied files and directories" long:"chown"`             //nolint:lll
	Chmod           string   `description:"Apply a mode to the copied files and directories"                        long:"chmod"`             //nolint:lll
	Platform        string   `description:"The platform to use"                                                     long:"platform"`          //nolint:lll
	BuildArgs       []string `description:"A build arg override passed on to a referenced earth target"             long:"build-arg"`         //nolint:lll
	IsDirCopy       bool     `description:"Copy entire directories, not just the contents"                          long:"dir"`               //nolint:lll
	KeepTs          bool     `description:"Keep created time file timestamps"                                       long:"keep-ts"`           //nolint:lll
	KeepOwn         bool     `description:"Keep owner info"                                                         long:"keep-own"`          //nolint:lll
	IfExists        bool     `description:"Do not fail if the artifact does not exist"                              long:"if-exists"`         //nolint:lll
	SymlinkNoFollow bool     `description:"Do not follow symlinks"                                                  long:"symlink-no-follow"` //nolint:lll
	AllowPrivileged bool     `description:"Allow targets to assume privileged mode"                                 long:"allow-privileged"`  //nolint:lll
	PassArgs        bool     `description:"Pass arguments to external targets"                                      long:"pass-args"`         //nolint:lll
}

// SaveArtifactOpts contains options for the SAVE ARTIFACT command.
type SaveArtifactOpts struct {
	KeepTs          bool `description:"Keep created time file timestamps"                                                                               long:"keep-ts"`           //nolint:lll
	KeepOwn         bool `description:"Keep owner info"                                                                                                 long:"keep-own"`          //nolint:lll
	IfExists        bool `description:"Do not fail if the artifact does not exist"                                                                      long:"if-exists"`         //nolint:lll
	SymlinkNoFollow bool `description:"Do not follow symlinks"                                                                                          long:"symlink-no-follow"` //nolint:lll
	Force           bool `description:"Force artifact to be saved, even if it means overwriting files or directories outside of the relative directory" long:"force"`             //nolint:lll
}

// SaveImageOpts contains options for the SAVE IMAGE command.
type SaveImageOpts struct {
	CacheFrom          []string `description:"Declare additional cache import as a Docker tag"                                                                    long:"cache-from"`             //nolint:lll
	Push               bool     `description:"Push the image to the remote registry provided that the build succeeds and also that earth is invoked in push mode" long:"push"`                   //nolint:lll
	CacheHint          bool     `description:"Instruct earth that the current target should be saved entirely as part of the remote cache"                        long:"cache-hint"`             //nolint:lll
	Insecure           bool     `description:"Use unencrypted connection for the push"                                                                            long:"insecure"`               //nolint:lll
	NoManifestList     bool     `description:"Do not include a manifest list (specifying the platform) in the creation of the image"                              long:"no-manifest-list"`       //nolint:lll
	WithoutEarthLabels bool     `description:"Disable build information dev.earthly labels to reduce the chance of changing images digests."                      long:"without-earthly-labels"` //nolint:lll
}

// BuildOpts contains options for the BUILD command.
type BuildOpts struct {
	Platforms       []string `description:"The platform to use"                                         long:"platform"`         //nolint:lll
	BuildArgs       []string `description:"A build arg override passed on to a referenced earth target" long:"build-arg"`        //nolint:lll
	AllowPrivileged bool     `description:"Allow targets to assume privileged mode"                     long:"allow-privileged"` //nolint:lll
	PassArgs        bool     `description:"Pass arguments to external targets"                          long:"pass-args"`        //nolint:lll
	AutoSkip        bool     `description:"Use auto-skip to bypass the target if nothing has changed"   long:"auto-skip"`        //nolint:lll
}

// GitCloneOpts contains options for the GIT CLONE command.
type GitCloneOpts struct {
	Branch string `description:"The git ref to use when cloning"   long:"branch"`
	KeepTs bool   `description:"Keep created time file timestamps" long:"keep-ts"`
}

// HealthCheckOpts contains options for the HEALTHCHECK command.
type HealthCheckOpts struct {
	Interval      time.Duration `default:"30s"                                                                                                       description:"The interval between healthchecks"                                long:"interval"`       //nolint:lll
	Timeout       time.Duration `default:"30s"                                                                                                       description:"The timeout before the command is considered failed"              long:"timeout"`        //nolint:lll
	StartPeriod   time.Duration `description:"An initialization time period in which failures are not counted towards the maximum number of retries" long:"start-period"`                                                                                  //nolint:lll
	Retries       int           `default:"3"                                                                                                         description:"The number of retries before a container is considered unhealthy" long:"retries"`        //nolint:lll
	StartInterval time.Duration `default:"5s"                                                                                                        description:"The time interval between health checks during the start period"  long:"start-interval"` //nolint:lll
}

// WithDockerOpts contains options for the WITH DOCKER command.
type WithDockerOpts struct {
	Platform        string   `description:"The platform to use"                                             long:"platform"` //nolint:lll
	CacheID         string   `description:"When specified, layer data will be persisted to specified cache" long:"cache-id"` //nolint:lll
	ComposeFiles    []string `description:"A compose file used to bring up services from"                   long:"compose"`  //nolint:lll
	ComposeServices []string `description:"A compose service to bring up"                                   long:"service"`  //nolint:lll
	Loads           []string `description:"An image produced by earth which is loaded as a Docker image"    long:"load"`
	BuildArgs       []string `description:"A build arg override passed on to a referenced earth target"     long:"build-arg"` //nolint:lll
	Pulls           []string `description:"An image which is pulled and made available in the docker cache" long:"pull"`
	AllowPrivileged bool     `description:"Allow targets referenced by load to assume privileged mode"      long:"allow-privileged"` //nolint:lll
	PassArgs        bool     `description:"Pass arguments to external targets"                              long:"pass-args"`        //nolint:lll
}

// DoOpts contains options for the DO command.
type DoOpts struct {
	AllowPrivileged bool `description:"Allow targets to assume privileged mode" long:"allow-privileged"`
	PassArgs        bool `description:"Pass arguments to external targets"      long:"pass-args"`
}

// ImportOpts contains options for the IMPORT command.
type ImportOpts struct {
	AllowPrivileged bool `description:"Allow targets to assume privileged mode" long:"allow-privileged"`
	PassArgs        bool `description:"Pass arguments to external targets"      long:"pass-args"`
}

// ArgOpts contains options for the ARG command.
type ArgOpts struct {
	Required bool `description:"Require argument to be non-empty"                       long:"required"`
	Global   bool `description:"Global argument to make available to all other targets" long:"global"`
}

// ProjectOpts contains options for the PROJECT command.
type ProjectOpts struct{}

// SetOpts contains options for the SET command.
type SetOpts struct{}

// LetOpts contains options for the LET command.
type LetOpts struct{}

// CacheOpts contains options for the CACHE command.
type CacheOpts struct {
	Sharing string `description:"The cache sharing mode: locked (default), shared, private"                 long:"sharing"`
	Mode    string `default:"0644"                                                                          description:"Apply a mode to the cache folder" long:"chmod"` //nolint:lll
	ID      string `description:"Cache ID, to reuse the same cache across different targets and Earthfiles" long:"id"`
	Persist bool   `description:"If should persist cache state in image"                                    long:"persist"`
}

// NewForOpts creates and returns a ForOpts with default separators.
func NewForOpts() ForOpts {
	return ForOpts{
		Separators: "\n\t ",
	}
}
