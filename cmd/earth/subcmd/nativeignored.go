package subcmd

import (
	"fmt"
	"sort"
	"strings"
)

// ignoredByNative is every flag the native engine does not act on, by struct
// field, with the spelling the user typed and what they lose.
//
// **Accepted-and-ignored is the one option that should not exist.** A flag the
// engine cannot honour can be refused or it can be reported, and either is a
// build whose author knows what they got; saying nothing is a build that quietly
// did something else. `--remote-cache` was the worst of them - a shared CI cache
// that was not shared, on a build that reported success.
//
// Reported rather than refused, because several of these are how a user drives
// the *other* engine and an invocation carrying them is ordinary: refusing would
// make one wrapper script unable to run both.
//
// `TestEveryFlagIsClassified` holds this complete: a flag that is neither read
// by the native path nor listed here fails the build of this package's tests,
// so the next one added has to be decided about rather than forgotten.
// noBuildkitd is the reason seven of these give, which is the same reason.
const noBuildkitd = "this engine runs no buildkitd"

var ignoredByNative = map[string]struct{ flag, lose string }{
	// Cache sharing. The costly ones: a build that believes it is sharing a
	// cache and is not looks like a slow engine rather than a missing feature.
	"RemoteCache":     {"--remote-cache", "this engine keeps no remote cache; the build runs from the local store"},
	"MaxRemoteCache":  {"--max-remote-cache", "there is no remote cache to write"},
	"UseInlineCache":  {"--use-inline-cache", "this engine reads no inline cache from an image"},
	"SaveInlineCache": {"--save-inline-cache", "this engine writes no inline cache into an image"},
	"SkipBuildkit":    {"--auto-skip", "this engine has no auto-skip; every step is planned"},
	"NoAutoSkip":      {"--no-auto-skip", "auto-skip is not implemented, so it is already off"},
	"LocalSkipDB":     {"--auto-skip-db-path", "auto-skip is not implemented"},

	// Output selection.
	"ArtifactMode": {"--artifact", "this engine takes a target, not an artifact reference"},
	"ImageMode":    {"--image", "this engine takes a target, not an image reference"},
	"Output":       {"--output", "output is decided by SAVE ARTIFACT AS LOCAL and --no-output"},

	// Fetching and git.
	"Pull":                 {"--pull", "a base image is pulled when the store lacks it, and pinned otherwise"},
	"GitBranchOverride":    {"--git-branch", "this engine resolves a remote target at the reference written"},
	"GitLFSPullInclude":    {"--git-lfs-pull-include", "this engine does not fetch LFS objects"},
	"GitUsernameOverride":  {"--git-username", "git credentials come from the environment's own helper"},
	"GitPasswordOverride":  {"--git-password", "git credentials come from the environment's own helper"},
	"SSHAuthSock":          {"--ssh-auth-sock", "this engine does not forward an agent into a step"},
	"EnvFile":              {"--env-file", "pass values with --build-arg, or set them in the environment"},
	"FeatureFlagOverrides": {"--version-flag-overrides", "VERSION flags are read from the Earthfile"},

	// Interactive and Dockerfile.
	"InteractiveDebugging": {"--interactive", "this engine has no interactive debugger"},
	"DockerfilePath":       {"--dockerfile", "FROM DOCKERFILE is not implemented"},

	// Scheduling.
	"ConversionParallelism": {"--conversion-parallelism", "this engine schedules from the graph"},
	"GlobalWaitEnd":         {"--global-wait-end", "an internal buildkit ordering flag"},
	"NoFakeDep":             {"--no-fake-dep", "this engine plants no fake dependency"},

	// Everything about running buildkitd, which this engine does not.
	"BuildkitHost":               {"--buildkit-host", noBuildkitd},
	"BuildkitdImage":             {"--buildkit-image", noBuildkitd},
	"BuildkitdSettings":          {"--buildkit-volume-name", noBuildkitd},
	"ContainerName":              {"--buildkit-container-name", noBuildkitd},
	"ContainerFrontend":          {"--container-frontend", "this engine needs no container runtime to build"},
	"NoBuildkitUpdate":           {"--no-buildkit-update", noBuildkitd},
	"BootstrapNoBuildkit":        {"--bootstrap-no-buildkit", noBuildkitd},
	"UseTickTockBuildkitImage":   {"--ticktock", noBuildkitd},
	"LocalRegistryHost":          {"--local-registry-host", "this engine needs no local registry"},
	"DisableRemoteRegistryProxy": {"--disable-remote-registry-proxy", "this engine proxies no registry"},
	"ServerConnTimeout":          {"--server-conn-timeout", "nothing reads this on either engine"},
	"Engine":                     {"--engine", "this build already chose the native engine"},
}

// notAboutTheBuild is read somewhere other than the build path - logging, the
// config file, the profiler - so it is honoured and has nothing to warn about.
//
// Listed rather than left out, so that `TestEveryFlagIsClassified` can insist
// every flag is decided about.
var notAboutTheBuild = map[string]bool{
	"ConfigPath": true, "InstallationName": true, "Debug": true, "Verbose": true,
	"EnableProfiler": true, "GithubAnnotations": true, "ExecStatsSummary": true,
	"LogstreamDebugFile": true, "LogstreamDebugManifestFile": true,
}

// ignoredNote is what to say about the flags this invocation *set* and this
// engine will not act on, or "" when there are none.
//
// One note however many there are: a build machine passing a handful of buildkit
// flags should not have the thing it asked for buried in a column of warnings.
//
// **Set, not non-zero.** Several of these have a default - a container name, a
// frontend, an engine - so a struct read after parsing cannot tell a value the
// user chose from one the flag set declares. Asking the parser instead is the
// only honest test, and reading the struct warned five flags at every
// invocation, none of them typed by anybody.
func ignoredNote(set func(string) bool) string {
	if set == nil {
		return ""
	}

	var said []string

	for _, what := range ignoredByNative {
		if !set(what.flag[len("--"):]) {
			continue
		}

		said = append(said, fmt.Sprintf("  %s is ignored: %s", what.flag, what.lose))
	}

	if len(said) == 0 {
		return ""
	}

	// Sorted, so two runs of one invocation report the same thing in the same
	// order - a warning that shuffles reads as two different warnings.
	sort.Strings(said)

	return "earthbuild: the native engine does not act on these:\n" + strings.Join(said, "\n")
}
