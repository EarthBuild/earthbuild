package image

// RememberChallengeForTest plants a remembered challenge, so a test can make one
// go stale without waiting for a registry to move its realm.
func RememberChallengeForTest(dir, key, at string) { rememberChallenge(dir, key, at) }
