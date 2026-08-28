package guest

// EnvDentryLimit is how many looked-up names a sandbox lets accumulate before
// it releases them.
//
// Zero turns the release off, which is what this engine did before it existed.
// Unset means the default below.
const EnvDentryLimit = "EARTH_GUEST_DENTRY_LIMIT"

// Declared apart from the mechanism that reads it, which is linux-only, because
// the host has to name it to forward it into the sandbox and the host is not
// linux. It was linux-only and therefore unforwarded, so on macOS the setting
// existed, was documented, and did nothing (E812).
