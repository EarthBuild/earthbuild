package reserved

const (
	EarthGitAuthor                  = "EARTH_GIT_AUTHOR"
	EarthGitAuthorEmail             = "EARTH_GIT_AUTHOR_EMAIL"
	EarthGitAuthorName              = "EARTH_GIT_AUTHOR_NAME"
	EarthGitBranch                  = "EARTH_GIT_BRANCH"
	EarthGitCoAuthors               = "EARTH_GIT_CO_AUTHORS"
	EarthGitCommitAuthorTimestamp   = "EARTH_GIT_COMMIT_AUTHOR_TIMESTAMP"
	EarthGitCommitTimestamp         = "EARTH_GIT_COMMIT_TIMESTAMP"
	EarthGitContentHash             = "EARTH_GIT_CONTENT_HASH"
	EarthGitHash                    = "EARTH_GIT_HASH"
	EarthGitOriginURL               = "EARTH_GIT_ORIGIN_URL"
	EarthGitOriginURLScrubbed       = "EARTH_GIT_ORIGIN_URL_SCRUBBED"
	EarthGitProjectName             = "EARTH_GIT_PROJECT_NAME"
	EarthGitRefs                    = "EARTH_GIT_REFS"
	EarthGitShortHash               = "EARTH_GIT_SHORT_HASH"
	EarthGitTag                     = "EARTH_GIT_TAG"
	EarthSourceDateEpoch            = "EARTH_SOURCE_DATE_EPOCH"
	EarthlyBuildSha                 = "EARTHLY_BUILD_SHA"
	EarthlyGitBranch                = "EARTHLY_GIT_BRANCH"
	EarthlyGitCommitTimestamp       = "EARTHLY_GIT_COMMIT_TIMESTAMP"
	EarthlyGitCommitAuthorTimestamp = "EARTHLY_GIT_COMMIT_AUTHOR_TIMESTAMP"
	EarthlyGitHash                  = "EARTHLY_GIT_HASH"
	EarthlyGitOriginURL             = "EARTHLY_GIT_ORIGIN_URL"
	EarthlyGitOriginURLScrubbed     = "EARTHLY_GIT_ORIGIN_URL_SCRUBBED"
	EarthlyGitProjectName           = "EARTHLY_GIT_PROJECT_NAME"
	EarthlyGitAuthor                = "EARTHLY_GIT_AUTHOR"
	EarthlyGitAuthorEmail           = "EARTHLY_GIT_AUTHOR_EMAIL"
	EarthlyGitAuthorName            = "EARTHLY_GIT_AUTHOR_NAME"
	EarthlyGitCoAuthors             = "EARTHLY_GIT_CO_AUTHORS"
	EarthlyGitShortHash             = "EARTHLY_GIT_SHORT_HASH"
	EarthlyGitTag                   = "EARTHLY_GIT_TAG"
	EarthlyGitRefs                  = "EARTHLY_GIT_REFS"
	EarthlyLocally                  = "EARTHLY_LOCALLY"
	EarthlyPush                     = "EARTHLY_PUSH"
	EarthlyCI                       = "EARTHLY_CI"
	EarthlyCIRunner                 = "EARTHLY_CI_RUNNER"
	EarthlySourceDateEpoch          = "EARTHLY_SOURCE_DATE_EPOCH"
	EarthlyTarget                   = "EARTHLY_TARGET"
	EarthlyTargetName               = "EARTHLY_TARGET_NAME"
	EarthlyTargetProject            = "EARTHLY_TARGET_PROJECT"
	EarthlyTargetProjectNoTag       = "EARTHLY_TARGET_PROJECT_NO_TAG"
	EarthlyTargetTag                = "EARTHLY_TARGET_TAG"
	EarthlyTargetTagDocker          = "EARTHLY_TARGET_TAG_DOCKER"
	EarthlyVersion                  = "EARTHLY_VERSION"
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
		EarthGitAuthor:                  {},
		EarthGitAuthorEmail:             {},
		EarthGitAuthorName:              {},
		EarthGitBranch:                  {},
		EarthGitCoAuthors:               {},
		EarthGitCommitAuthorTimestamp:   {},
		EarthGitCommitTimestamp:         {},
		EarthGitContentHash:             {},
		EarthGitHash:                    {},
		EarthGitOriginURL:               {},
		EarthGitOriginURLScrubbed:       {},
		EarthGitProjectName:             {},
		EarthGitRefs:                    {},
		EarthGitShortHash:               {},
		EarthGitTag:                     {},
		EarthSourceDateEpoch:            {},
		EarthlyBuildSha:                 {},
		EarthlyGitBranch:                {},
		EarthlyGitCommitTimestamp:       {},
		EarthlyGitCommitAuthorTimestamp: {},
		EarthlyGitAuthor:                {},
		EarthlyGitAuthorEmail:           {},
		EarthlyGitAuthorName:            {},
		EarthlyGitCoAuthors:             {},
		EarthlyGitHash:                  {},
		EarthlyGitOriginURL:             {},
		EarthlyGitOriginURLScrubbed:     {},
		EarthlyGitProjectName:           {},
		EarthlyGitShortHash:             {},
		EarthlyGitTag:                   {},
		EarthlyGitRefs:                  {},
		EarthlyLocally:                  {},
		EarthlyPush:                     {},
		EarthlyCI:                       {},
		EarthlyCIRunner:                 {},
		EarthlySourceDateEpoch:          {},
		EarthlyTarget:                   {},
		EarthlyTargetName:               {},
		EarthlyTargetProject:            {},
		EarthlyTargetProjectNoTag:       {},
		EarthlyTargetTag:                {},
		EarthlyTargetTagDocker:          {},
		EarthlyVersion:                  {},
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

// IsBuiltIn returns true if s is the name of a builtin arg.
func IsBuiltIn(s string) bool {
	_, exists := args[s]
	return exists
}
