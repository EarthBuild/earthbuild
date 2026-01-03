package features

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/ast/spec"
	goflags "github.com/jessevdk/go-flags"
	"github.com/pkg/errors"

	"github.com/EarthBuild/earthbuild/util/flagutil"
)

// Features is used to denote which features to flip on or off; this is for use in maintaining
// backwards compatibility.
type Features struct {
	// Never enabled by default
	NoUseRegistryForWithDocker bool `description:"disable use-registry-for-with-docker" long:"no-use-registry-for-with-docker"` // escape hatch for disabling WITH DOCKER registry, e.g. used by eine-based tests
	EarthlyCIRunnerArg         bool `description:"includes EARTHLY_CI_RUNNER ARG"       long:"earthly-ci-runner-arg"`           // earthly CI was discontinued, no reason to enable this by default

	// VERSION 0.5
	ExecAfterParallel        bool `description:"force execution after parallel conversion"                    enabled_in_version:"0.5" long:"exec-after-parallel"`
	ParallelLoad             bool `description:"perform parallel loading of images into WITH DOCKER"          enabled_in_version:"0.5" long:"parallel-load"`
	UseRegistryForWithDocker bool `description:"use embedded Docker registry for WITH DOCKER load operations" enabled_in_version:"0.5" long:"use-registry-for-with-docker"`

	// VERSION 0.6
	ForIn                      bool `description:"allow the use of the FOR command"                                                                                                                 enabled_in_version:"0.6" long:"for-in"`
	NoImplicitIgnore           bool `description:"disable implicit ignore rules to exclude .tmp-earthly-out/, build.earth, Earthfile, .earthignore and .earthlyignore when resolving local context" enabled_in_version:"0.6" long:"no-implicit-ignore"`
	ReferencedSaveOnly         bool `description:"only save artifacts that are directly referenced"                                                                                                 enabled_in_version:"0.6" long:"referenced-save-only"`
	RequireForceForUnsafeSaves bool `description:"require the --force flag when saving to path outside of current path"                                                                             enabled_in_version:"0.6" long:"require-force-for-unsafe-saves"`
	UseCopyIncludePatterns     bool `description:"specify an include pattern to buildkit when performing copies"                                                                                    enabled_in_version:"0.6" long:"use-copy-include-patterns"`

	// VERSION 0.7
	CheckDuplicateImages     bool `description:"check for duplicate images during output"                                        enabled_in_version:"0.7" long:"check-duplicate-images"`
	EarthlyCIArg             bool `description:"include EARTHLY_CI arg"                                                          enabled_in_version:"0.7" long:"ci-arg"`
	EarthlyGitAuthorArgs     bool `description:"includes EARTHLY_GIT_AUTHOR and EARTHLY_GIT_CO_AUTHORS ARGs"                     enabled_in_version:"0.7" long:"earthly-git-author-args"`
	EarthlyLocallyArg        bool `description:"includes EARTHLY_LOCALLY ARG"                                                    enabled_in_version:"0.7" long:"earthly-locally-arg"`
	EarthlyVersionArg        bool `description:"includes EARTHLY_VERSION and EARTHLY_BUILD_SHA ARGs"                             enabled_in_version:"0.7" long:"earthly-version-arg"`
	ExplicitGlobal           bool `description:"require base target args to have explicit settings to be considered global args" enabled_in_version:"0.7" long:"explicit-global"`
	GitCommitAuthorTimestamp bool `description:"include EARTHLY_GIT_COMMIT_AUTHOR_TIMESTAMP arg"                                 enabled_in_version:"0.7" long:"git-commit-author-timestamp"`
	NewPlatform              bool `description:"enable new platform behavior"                                                    enabled_in_version:"0.7" long:"new-platform"`
	NoTarBuildOutput         bool `description:"do not print output when creating a tarball to load into WITH DOCKER"            enabled_in_version:"0.7" long:"no-tar-build-output"`
	SaveArtifactKeepOwn      bool `description:"always apply the --keep-own flag with SAVE ARTIFACT"                             enabled_in_version:"0.7" long:"save-artifact-keep-own"`
	ShellOutAnywhere         bool `description:"allow shelling-out in the middle of ARGs, or any other command"                  enabled_in_version:"0.7" long:"shell-out-anywhere"`
	UseCacheCommand          bool `description:"allow use of CACHE command in Earthfiles"                                        enabled_in_version:"0.7" long:"use-cache-command"`
	UseChmod                 bool `description:"enable the COPY --chmod option"                                                  enabled_in_version:"0.7" long:"use-chmod"`
	UseCopyLink              bool `description:"use the equivalent of COPY --link for all copy-like operations"                  enabled_in_version:"0.7" long:"use-copy-link"`
	UseHostCommand           bool `description:"allow use of HOST command in Earthfiles"                                         enabled_in_version:"0.7" long:"use-host-command"`
	UseNoManifestList        bool `description:"enable the SAVE IMAGE --no-manifest-list option"                                 enabled_in_version:"0.7" long:"use-no-manifest-list"`
	UseProjectSecrets        bool `description:"enable project-based secret resolution"                                          enabled_in_version:"0.7" long:"use-project-secrets"`
	WaitBlock                bool `description:"enable WITH/END feature, also allows RUN --push mixed with non-push commands"    enabled_in_version:"0.7" long:"wait-block"`

	// VERSION 0.8
	NoNetwork                       bool `description:"allow the use of RUN --network=none commands"                                                                                                                                              enabled_in_version:"0.8" long:"no-network"`
	ArgScopeSet                     bool `description:"enable SET to reassign ARGs and prevent ARGs from being redeclared in the same scope"                                                                                                      enabled_in_version:"0.8" long:"arg-scope-and-set"`
	UseDockerIgnore                 bool `description:"fallback to .dockerignore incase .earthlyignore or .earthlyignore do not exist in a local \"FROM DOCKERFILE\" target"                                                                      enabled_in_version:"0.8" long:"use-docker-ignore"`
	PassArgs                        bool `description:"Allow the use of the --pass-arg flag in FROM, BUILD, COPY, WITH DOCKER, and DO commands"                                                                                                   enabled_in_version:"0.8" long:"pass-args"`
	GlobalCache                     bool `description:"enable global caches (shared across different Earthfiles), for cache mounts and CACHEs having an ID"                                                                                       enabled_in_version:"0.8" long:"global-cache"`
	CachePersistOption              bool `description:"Adds option to persist caches, Changes default CACHE behaviour to not persist"                                                                                                             enabled_in_version:"0.8" long:"cache-persist-option"`
	GitRefs                         bool `description:"includes EARTHLY_GIT_REFS ARG"                                                                                                                                                             enabled_in_version:"0.8" long:"git-refs"`
	UseVisitedUpfrontHashCollection bool `description:"Uses a new target visitor implementation that computes upfront the hash of the visited targets and adds support for running all targets with the same name but different args in parallel" enabled_in_version:"0.8" long:"use-visited-upfront-hash-collection"`
	UseFunctionKeyword              bool `description:"Use the FUNCTION key word instead of COMMAND"                                                                                                                                              enabled_in_version:"0.8" long:"use-function-keyword"`

	// unreleased
	TryFinally                    bool `description:"allow the use of the TRY/FINALLY commands"                                   long:"try"`
	WildcardBuilds                bool `description:"allow for the expansion of wildcard (glob) paths for BUILD commands"         long:"wildcard-builds"`
	BuildAutoSkip                 bool `description:"allow for --auto-skip to be used on individual BUILD commands"               long:"build-auto-skip"`
	AllowPrivilegedFromDockerfile bool `description:"Allow the use of the --allow-privileged flag in the FROM DOCKERFILE command" long:"allow-privileged-from-dockerfile"`
	RunWithAWS                    bool `description:"make AWS credentials in the environment or ~/.aws available to RUN commands" long:"run-with-aws"`
	WildcardCopy                  bool `description:"allow for the expansion of wildcard (glob) paths for COPY commands"          long:"wildcard-copy"`
	RawOutput                     bool `description:"allow for --raw-output on RUN commands"                                      long:"raw-output"`
	GitAuthorEmailNameArgs        bool `description:"includes EARTHLY_GIT_AUTHOR_EMAIL and EARTHLY_GIT_AUTHOR_NAME builtin ARGs"  long:"git-author-email-name-args"`
	AllowWithoutEarthlyLabels     bool `description:"Allow the usage of --without-earthly-labels in SAVE IMAGE"                   long:"allow-without-earthly-labels"`
	DockerCache                   bool `description:"enable the WITH DOCKER --cache-id option"                                    long:"docker-cache"`
	RunWithAWSOIDC                bool `description:"make AWS credentials via OIDC provider available to RUN commands"            long:"run-with-aws-oidc"`

	// version numbers
	Major int
	Minor int
}

type ctxKey struct{}

// Version returns the current version.
func (f *Features) Version() string {
	return fmt.Sprintf("%d.%d", f.Major, f.Minor)
}

func parseFlagOverrides(env string) map[string]string {
	env = strings.TrimSpace(env)
	m := map[string]string{}
	if env != "" {
		for _, flag := range strings.Split(env, ",") {
			flagNameAndValue := strings.SplitN(flag, "=", 2)
			var flagValue string
			flagName := strings.TrimSpace(flagNameAndValue[0])
			flagName = strings.TrimPrefix(flagName, "--")
			if len(flagNameAndValue) > 1 {
				flagValue = strings.TrimSpace(flagNameAndValue[1])
			}
			m[flagName] = flagValue
		}
	}
	return m
}

// String returns a string representation of the version and set flags.
func (f *Features) String() string {
	if f == nil {
		return "<nil>"
	}

	v := reflect.ValueOf(*f)
	typeOf := v.Type()

	flags := []string{}
	for i := range typeOf.NumField() {
		tag := typeOf.Field(i).Tag
		if flagName, ok := tag.Lookup("long"); ok {
			ifaceVal := v.Field(i).Interface()
			if boolVal, ok := ifaceVal.(bool); ok && boolVal {
				flags = append(flags, fmt.Sprintf("--%v", flagName))
			}
		}
	}
	sort.Strings(flags)
	args := []string{"VERSION"}
	if len(flags) > 0 {
		args = append(args, strings.Join(flags, " "))
	}
	args = append(args, fmt.Sprintf("%d.%d", f.Major, f.Minor))
	return strings.Join(args, " ")
}

// ApplyFlagOverrides parses a comma separated list of feature flag overrides (without the -- flag name prefix)
// and sets them in the referenced features.
func ApplyFlagOverrides(ftrs *Features, envOverrides string) error {
	overrides := parseFlagOverrides(envOverrides)

	fieldIndices := map[string]int{}
	typeOf := reflect.ValueOf(*ftrs).Type()
	for i := range typeOf.NumField() {
		f := typeOf.Field(i)
		tag := f.Tag
		if flagName, ok := tag.Lookup("long"); ok {
			fieldIndices[flagName] = i
		}
	}

	ftrsStruct := reflect.ValueOf(ftrs).Elem()
	for key := range overrides {
		i, ok := fieldIndices[key]
		if !ok {
			return fmt.Errorf("unable to set %s: invalid flag", key)
		}
		fv := ftrsStruct.Field(i)
		if fv.IsValid() && fv.CanSet() {
			fv.SetBool(true)
		} else {
			return fmt.Errorf("unable to set %s: field is invalid or cant be set", key)
		}
		ifaceVal := fv.Interface()
		if _, ok := ifaceVal.(bool); ok {
			fv.SetBool(true)
		} else {
			return fmt.Errorf("unable to set %s: only boolean fields are currently supported", key)
		}
	}
	processNegativeFlags(ftrs)
	return nil
}

var errUnexpectedArgs = errors.New("unexpected VERSION arguments; should be VERSION [flags] <major-version>.<minor-version>")

func instrumentVersion(_ string, opt *goflags.Option, s *string) (*string, error) {
	return s, nil // don't modify the flag, just pass it back.
}

// Get returns a features struct for a particular version.
func Get(version *spec.Version) (*Features, bool, error) {
	var ftrs Features
	hasVersion := (version != nil)
	if !hasVersion {
		// If no version is specified, we default to 0.5 (the Earthly version
		// before the VERSION command was introduced).
		version = &spec.Version{
			Args: []string{"0.5"},
		}
	}

	if version.Args == nil {
		return nil, false, errUnexpectedArgs
	}

	parsedArgs, err := flagutil.ParseArgsWithValueModifierAndOptions("VERSION", &ftrs, version.Args, instrumentVersion, goflags.PassDoubleDash|goflags.PassAfterNonOption)
	if err != nil {
		return nil, false, err
	}

	if len(parsedArgs) != 1 {
		return nil, false, errUnexpectedArgs
	}

	versionValueStr := parsedArgs[0]
	majorAndMinor := strings.Split(versionValueStr, ".")
	if len(majorAndMinor) != 2 {
		return nil, false, errUnexpectedArgs
	}
	ftrs.Major, err = strconv.Atoi(majorAndMinor[0])
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed to parse major version %q", majorAndMinor[0])
	}
	ftrs.Minor, err = strconv.Atoi(majorAndMinor[1])
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed to parse minor version %q", majorAndMinor[1])
	}

	return &ftrs, hasVersion, nil
}

// versionAtLeast returns true if the version configured in `ftrs`
// are greater than or equal to the provided major and minor versions.
func versionAtLeast(ftrs Features, majorVersion, minorVersion int) bool {
	return (ftrs.Major > majorVersion) || (ftrs.Major == majorVersion && ftrs.Minor >= minorVersion)
}

func processNegativeFlags(ftrs *Features) {
	if ftrs.NoUseRegistryForWithDocker {
		ftrs.UseRegistryForWithDocker = false
	}
}

// WithContext adds the current *Features into the given context and returns a new context.
// Trying to add the *Features to the context more than once will result in an error.
func (f *Features) WithContext(ctx context.Context) (context.Context, error) {
	if ctx.Value(ctxKey{}) != nil {
		return ctx, errors.New("features is already set")
	}
	return context.WithValue(ctx, ctxKey{}, f), nil
}

// FromContext returns the *Features associated with the ctx.
// If no features is found, nil is returned.
func FromContext(ctx context.Context) *Features {
	if f, ok := ctx.Value(ctxKey{}).(*Features); ok {
		return f
	}
	return nil
}

func (f *Features) ProcessFlags() ([]string, error) {
	warningStrs := make([]string, 0)

	v := reflect.ValueOf(f).Elem()
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)
		value := v.Field(i)
		version := field.Tag.Get("enabled_in_version")
		if len(version) == 0 {
			continue
		}
		majorVersion, minorVersion := mustParseVersion(field.Tag.Get("enabled_in_version"))
		if versionAtLeast(*f, majorVersion, minorVersion) && value.Kind() == reflect.Bool {
			if value.Bool() {
				tagName := field.Tag.Get("long")
				warningStrs = append(warningStrs, "--"+strings.ToLower(tagName))
			}
			value.SetBool(true)
		}
	}

	processNegativeFlags(f)

	if f.ArgScopeSet && !f.ShellOutAnywhere {
		// ArgScopeSet uses new ARG declaration logic that requires
		// ShellOutAnywhere. We're erroring here to ensure that users get that
		// feedback as early as possible.
		return nil, errors.New("--arg-scope-and-set requires --shell-out-anywhere")
	}

	return warningStrs, nil
}

func mustParseVersion(version string) (int, int) {
	parts := strings.Split(version, ".")
	if len(parts) != 2 {
		panic("invalid version format: " + version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		panic("invalid major version: " + parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		panic("invalid minor version: " + parts[1])
	}

	return major, minor
}
