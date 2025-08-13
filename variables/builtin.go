package variables

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/containerd/containerd/platforms"

	"github.com/earthbuild/earthbuild/domain"
	"github.com/earthbuild/earthbuild/features"
	"github.com/earthbuild/earthbuild/util/gitutil"
	"github.com/earthbuild/earthbuild/util/llbutil"
	"github.com/earthbuild/earthbuild/util/platutil"
	"github.com/earthbuild/earthbuild/util/stringutil"
	arg "github.com/earthbuild/earthbuild/variables/reserved"
)

// DefaultArgs contains additional builtin ARG values which need
// to be passed in from outside of the scope of this package.
type DefaultArgs struct {
	earthbuildVersion  string
	earthbuildBuildSha string
}

// BuiltinArgs returns a scope containing the builtin args.
func BuiltinArgs(target domain.Target, platr *platutil.Resolver, gitMeta *gitutil.GitMetadata, defaultArgs DefaultArgs, ftrs *features.Features, push bool, ci bool) *Scope {
	ret := NewScope()
	ret.Add(arg.earthbuildTarget, target.StringCanonical())
	ret.Add(arg.earthbuildTargetProject, target.ProjectCanonical())
	targetNoTag := target
	targetNoTag.Tag = ""
	ret.Add(arg.earthbuildTargetProjectNoTag, targetNoTag.ProjectCanonical())
	ret.Add(arg.earthbuildTargetName, target.Target)

	setTargetTag(ret, target, gitMeta)

	if platr != nil {
		SetPlatformArgs(ret, platr)
		setUserPlatformArgs(ret, platr)
		if ftrs.NewPlatform {
			setNativePlatformArgs(ret, platr)
		}
	}

	if ftrs.WaitBlock {
		ret.Add(arg.earthbuildPush, fmt.Sprintf("%t", push))
	}

	if ftrs.earthbuildVersionArg {
		ret.Add(arg.earthbuildVersion, defaultArgs.earthbuildVersion)
		ret.Add(arg.earthbuildBuildSha, defaultArgs.earthbuildBuildSha)
	}

	if ftrs.earthbuildCIArg {
		ret.Add(arg.earthbuildCI, fmt.Sprintf("%t", ci))
	}

	if ftrs.earthbuildLocallyArg {
		SetLocally(ret, false)
	}

	if gitMeta != nil {
		ret.Add(arg.earthbuildGitHash, gitMeta.Hash)
		ret.Add(arg.earthbuildGitShortHash, gitMeta.ShortHash)
		branch := ""
		if len(gitMeta.Branch) > 0 {
			branch = gitMeta.Branch[0]
		}
		ret.Add(arg.earthbuildGitBranch, branch)
		tag := ""
		if len(gitMeta.Tags) > 0 {
			tag = gitMeta.Tags[0]
		}
		ret.Add(arg.earthbuildGitTag, tag)
		ret.Add(arg.earthbuildGitOriginURL, gitMeta.RemoteURL)
		ret.Add(arg.earthbuildGitOriginURLScrubbed, stringutil.ScrubCredentials(gitMeta.RemoteURL))
		ret.Add(arg.earthbuildGitProjectName, getProjectName(gitMeta.RemoteURL))
		ret.Add(arg.earthbuildGitCommitTimestamp, gitMeta.CommitterTimestamp)

		if ftrs.GitCommitAuthorTimestamp {
			ret.Add(arg.earthbuildGitCommitAuthorTimestamp, gitMeta.AuthorTimestamp)
		}
		if gitMeta.CommitterTimestamp == "" {
			ret.Add(arg.earthbuildSourceDateEpoch, "0")
		} else {
			ret.Add(arg.earthbuildSourceDateEpoch, gitMeta.CommitterTimestamp)
		}
		if ftrs.earthbuildGitAuthorArgs {
			ret.Add(arg.earthbuildGitAuthor, gitMeta.AuthorEmail)
			ret.Add(arg.earthbuildGitCoAuthors, strings.Join(gitMeta.CoAuthors, " "))
		}
		if ftrs.GitAuthorEmailNameArgs {
			if gitMeta.AuthorName != "" && gitMeta.AuthorEmail != "" {
				ret.Add(arg.earthbuildGitAuthor, fmt.Sprintf("%s <%s>", gitMeta.AuthorName, gitMeta.AuthorEmail))
			}
			ret.Add(arg.earthbuildGitAuthorEmail, gitMeta.AuthorEmail)
			ret.Add(arg.earthbuildGitAuthorName, gitMeta.AuthorName)
		}

		if ftrs.GitRefs {
			ret.Add(arg.earthbuildGitRefs, strings.Join(gitMeta.Refs, " "))
		}
	} else {
		// Ensure SOURCE_DATE_EPOCH is always available
		ret.Add(arg.earthbuildSourceDateEpoch, "0")
	}

	if ftrs.earthbuildCIRunnerArg {
		ret.Add(arg.earthbuildCIRunner, strconv.FormatBool(false))
	}
	return ret
}

// SetPlatformArgs sets the platform-specific built-in args to a specific platform.
func SetPlatformArgs(s *Scope, platr *platutil.Resolver) {
	platform := platr.Materialize(platr.Current())
	llbPlatform := platr.ToLLBPlatform(platform)
	s.Add(arg.TargetPlatform, platform.String())
	s.Add(arg.TargetOS, llbPlatform.OS)
	s.Add(arg.TargetArch, llbPlatform.Architecture)
	s.Add(arg.TargetVariant, llbPlatform.Variant)
}

func setUserPlatformArgs(s *Scope, platr *platutil.Resolver) {
	platform := platr.LLBUser()
	s.Add(arg.UserPlatform, platforms.Format(platform))
	s.Add(arg.UserOS, platform.OS)
	s.Add(arg.UserArch, platform.Architecture)
	s.Add(arg.UserVariant, platform.Variant)
}

func setNativePlatformArgs(s *Scope, platr *platutil.Resolver) {
	platform := platr.LLBNative()
	s.Add(arg.NativePlatform, platforms.Format(platform))
	s.Add(arg.NativeOS, platform.OS)
	s.Add(arg.NativeArch, platform.Architecture)
	s.Add(arg.NativeVariant, platform.Variant)
}

// SetLocally sets the locally built-in arg value
func SetLocally(s *Scope, locally bool) {
	s.Add(arg.earthbuildLocally, fmt.Sprintf("%v", locally))
}

// getProjectName returns the deprecated PROJECT_NAME value
func getProjectName(s string) string {
	protocol := "unknown"
	parts := strings.SplitN(s, "://", 2)
	if len(parts) > 1 {
		protocol = parts[0]
		s = parts[1]
	}
	parts = strings.SplitN(s, "@", 2)
	if len(parts) > 1 {
		s = parts[1]
	}
	if protocol == "unknown" {
		s = strings.Replace(s, ":", "/", 1)
	}
	s = strings.TrimSuffix(s, ".git")
	parts = strings.SplitN(s, "/", 2)
	if len(parts) > 1 {
		s = parts[1]
	}
	return s
}

func setTargetTag(ret *Scope, target domain.Target, gitMeta *gitutil.GitMetadata) {
	// We prefer branch for these tags if the build is triggered from an action on a branch (pr / push)
	// https://github.com/earthbuild/cloud-issues/issues/11#issuecomment-1467308267
	if gitMeta != nil && gitMeta.BranchOverrideTagArg && len(gitMeta.Branch) > 0 {
		branch := gitMeta.Branch[0]
		ret.Add(arg.earthbuildTargetTag, branch)
		ret.Add(arg.earthbuildTargetTagDocker, llbutil.DockerTagSafe(branch))
		return
	}
	ret.Add(arg.earthbuildTargetTag, target.Tag)
	ret.Add(arg.earthbuildTargetTagDocker, llbutil.DockerTagSafe(target.Tag))
}
