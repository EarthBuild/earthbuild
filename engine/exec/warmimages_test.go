package exec

import (
	"context"
	"testing"
)

// A build with no sandbox must still survive being warmed.
//
// `WarmImages` reaches for the sandbox's store directory to find the challenge
// cache, and a local-only build has no sandbox at all - so the first version of
// this panicked on a nil dereference, caught by TestALocalOnlyBuildNeedsNoSandbox
// rather than by anything written for it. Pinned directly here, because the
// relationship between "no machine" and "warm the registry anyway" is not
// obvious from either side.
func TestWarmImagesSurvivesHavingNoSandbox(t *testing.T) {
	t.Parallel()

	e := &Executor{}

	// Nothing to warm: must not touch the sandbox it does not have.
	e.WarmImages(context.Background(), nil, "linux/arm64")

	// Something to warm, still no sandbox. The reference is unroutable on
	// purpose - what is under test is that asking cannot panic, not that a
	// registry answers.
	e.WarmImages(context.Background(), []string{"127.0.0.1:1/library/x:1"}, "linux/arm64")
}
