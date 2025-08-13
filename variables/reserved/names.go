package reserved

const (
	earthbuildBuildSha                 = "EARTHLY_BUILD_SHA"
	earthbuildGitBranch                = "earthbuild_GIT_BRANCH"
	earthbuildGitCommitTimestamp       = "earthbuild_GIT_COMMIT_TIMESTAMP"
	earthbuildGitCommitAuthorTimestamp = "earthbuild_GIT_COMMIT_AUTHOR_TIMESTAMP"
	earthbuildGitHash                  = "EARTHLY_GIT_HASH"
	earthbuildGitOriginURL             = "EARTHLY_GIT_ORIGIN_URL"
	earthbuildGitOriginURLScrubbed     = "EARTHLY_GIT_ORIGIN_URL_SCRUBBED"
	earthbuildGitProjectName           = "EARTHLY_GIT_PROJECT_NAME"
	earthbuildGitAuthor                = "earthbuild_GIT_AUTHOR"
	earthbuildGitAuthorEmail           = "earthbuild_GIT_AUTHOR_EMAIL"
	earthbuildGitAuthorName            = "earthbuild_GIT_AUTHOR_NAME"
	earthbuildGitCoAuthors             = "earthbuild_GIT_CO_AUTHORS"
	earthbuildGitShortHash             = "earthbuild_GIT_SHORT_HASH"
	earthbuildGitTag                   = "earthbuild_GIT_TAG"
	earthbuildGitRefs                  = "earthbuild_GIT_REFS"
	earthbuildLocally                  = "EARTHLY_LOCALLY"
	earthbuildPush                     = "EARTHLY_PUSH"
	earthbuildCI                       = "EARTHLY_CI"
	earthbuildCIRunner                 = "EARTHLY_CI_RUNNER"
	earthbuildSourceDateEpoch          = "EARTHLY_SOURCE_DATE_EPOCH"
	earthbuildTarget                   = "EARTHLY_TARGET"
	earthbuildTargetName               = "EARTHLY_TARGET_NAME"
	earthbuildTargetProject            = "EARTHLY_TARGET_PROJECT"
	earthbuildTargetProjectNoTag       = "EARTHLY_TARGET_PROJECT_NO_TAG"
	earthbuildTargetTag                = "EARTHLY_TARGET_TAG"
	earthbuildTargetTagDocker          = "EARTHLY_TARGET_TAG_DOCKER"
	earthbuildVersion                  = "EARTHLY_VERSION"
	NativeArch                      = "NATIVEARCH"
	NativeOS                        = "NATIVEOS"
	NativePlatform                  = "NATIVEPLATFORM"
	NativeVariant                   = "NATIVEVARIANT"
	TargetArch                      = "TARGETARCH"
	TargetOS                        = "TARGETOS"
	TargetPlatform                  = "TARGETPLATFORM"
	TargetVariant                   = "TARGETVARIANT"
	UserArch                        = "USERARCH"
	UserOS                          = "USEROS"
	UserPlatform                    = "USERPLATFORM"
	UserVariant                     = "USERVARIANT"
)

var args map[string]struct{}

func init() {
	args = map[string]struct{}{
		earthbuildBuildSha:                 {},
		earthbuildGitBranch:                {},
		earthbuildGitCommitTimestamp:       {},
		earthbuildGitCommitAuthorTimestamp: {},
		earthbuildGitAuthor:                {},
		earthbuildGitAuthorEmail:           {},
		earthbuildGitAuthorName:            {},
		earthbuildGitCoAuthors:             {},
		earthbuildGitHash:                  {},
		earthbuildGitOriginURL:             {},
		earthbuildGitOriginURLScrubbed:     {},
		earthbuildGitProjectName:           {},
		earthbuildGitShortHash:             {},
		earthbuildGitTag:                   {},
		earthbuildGitRefs:                  {},
		earthbuildLocally:                  {},
		earthbuildPush:                     {},
		earthbuildCI:                       {},
		earthbuildCIRunner:                 {},
		earthbuildSourceDateEpoch:          {},
		earthbuildTarget:                   {},
		earthbuildTargetName:               {},
		earthbuildTargetProject:            {},
		earthbuildTargetProjectNoTag:       {},
		earthbuildTargetTag:                {},
		earthbuildTargetTagDocker:          {},
		earthbuildVersion:                  {},
		NativeArch:                      {},
		NativeOS:                        {},
		NativePlatform:                  {},
		NativeVariant:                   {},
		TargetArch:                      {},
		TargetOS:                        {},
		TargetPlatform:                  {},
		TargetVariant:                   {},
		UserArch:                        {},
		UserOS:                          {},
		UserPlatform:                    {},
		UserVariant:                     {},
	}
}

// IsBuiltIn returns true if s is the name of a builtin arg
func IsBuiltIn(s string) bool {
	_, exists := args[s]
	return exists
}
