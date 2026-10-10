//go:build linux

package guest

import (
	"os"
	"sync"
)

// stepLinkKind is how a step's interface hangs off the guest's own.
//
// Read once, for the reason privateStepNet is: a build that answered
// differently for two steps would put some of them on a segment they can use
// and some on one they cannot.
var stepLinkKind = sync.OnceValue(func() string {
	if os.Getenv(EnvStepLink) == LinkIPVLAN {
		return LinkIPVLAN
	}

	return LinkMACVLAN
})
