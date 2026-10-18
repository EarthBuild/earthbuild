package guest

import "strings"

// SandboxHost is the name a step knows itself by.
//
// **A constant, because the alternative is not reproducible.** A step inherits
// the machine's hostname unless something sets one, so a build that records
// where it ran - and many do: `uname -n`, JAR manifests, RPM headers, configure
// scripts - produced different bytes on different machines while its key said
// they were the same. A constant makes that field a constant too (I3).
//
// The reference engine's name, kept. The corpus pings it to check a hosts file
// is working, tools grep build logs for it, and a post-buildkit engine that
// renamed it would break those for a word. Changing it is a decision about what
// users see, not an implementation detail.
const SandboxHost = "buildkitsandbox"

// hostsFile is the `/etc/hosts` a step gets, or empty where it declared none.
//
// **Written, not merged.** An image ships its own `/etc/hosts`, and a step that
// resolved by a merged file would resolve differently depending on what its base
// happened to contain - ambient state no key describes (I3). What a step
// resolves by is a function of what the Earthfile said and nothing else.
//
// Localhost is included because a hosts file without it breaks everything that
// resolves `localhost`, and the entries an Earthfile declares are additions to a
// working resolver rather than a replacement for one.
//
// The address comes first: that is the file's format, and a line written the
// other way round resolves nothing while looking correct.
func hostsFile(entries []string) string {
	var b strings.Builder

	b.WriteString("127.0.0.1\tlocalhost\n::1\tlocalhost ip6-localhost ip6-loopback\n")

	// The step's own name, which is the machine's to a program that asks the
	// kernel and nobody's at all to one that then tries to resolve it. See
	// SandboxHost.
	b.WriteString("127.0.0.1\t" + SandboxHost + "\n")

	for _, e := range entries {
		name, address, ok := strings.Cut(e, " ")
		if !ok {
			continue
		}

		b.WriteString(address + "\t" + name + "\n")
	}

	return b.String()
}

// hostsMount is the `/etc/hosts` a step gets.
//
// **Unconditional since E768, and that is the whole of the fix.** It used to be
// produced only where an Earthfile declared `HOST` entries, so a step that
// declared none kept its image's file - which does not name the sandbox. Once
// the sandbox had a name (E758), `earth-entrypoint.sh` derived
// `EARTH_BUILDKIT_HOST=tcp://$(hostname):8372` from it, and the inner build
// dialled a name nothing resolved. Five Native jobs timed out for a minute
// each waiting for it.
//
// Written rather than merged, as before: what a step resolves by is a function
// of what the Earthfile said, plus the two things every step is entitled to -
// localhost, and its own name.
//
// Separated from the file's contents so that *whether a step gets one* can be
// asserted without a running guest: the mutation sweep found the composition
// unguarded, because the only test of it was behind the `integration` tag and
// the sweep does not build with tags (E415).
//
// It carries its contents rather than an id because there is nothing in any
// store to point at - the same shape a secret uses, and the first mount that is
// *only* its contents.
func hostsMount(entries []string) []Mount {
	contents := hostsFile(entries)
	if contents == "" {
		return nil
	}

	return []Mount{{Target: "/etc/hosts", Secret: contents}}
}
