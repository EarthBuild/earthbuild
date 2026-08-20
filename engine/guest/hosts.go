package guest

import "strings"

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
	if len(entries) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("127.0.0.1\tlocalhost\n::1\tlocalhost ip6-localhost ip6-loopback\n")

	for _, e := range entries {
		name, address, ok := strings.Cut(e, " ")
		if !ok {
			continue
		}

		b.WriteString(address + "\t" + name + "\n")
	}

	return b.String()
}

// hostsMount is the mount a step's declared entries travel in, or none.
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
