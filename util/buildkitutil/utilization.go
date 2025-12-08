package buildkitutil

import (
	"fmt"
	"strconv"
)

// FormatUtilization returns a string representing utilization
func FormatUtilization(numOtherSessions, load, maxParallelism int) string {
	otherSessions := "unknown"
	if numOtherSessions >= 0 {
		otherSessions = strconv.Itoa(numOtherSessions)
	}
	return fmt.Sprintf("Utilization: %s other builds, %d/%d op load", otherSessions, load, maxParallelism)
}
