package image

import "strings"

// FullReference expands an image reference to the form a runtime matches on.
//
// `built-here:v1` is how an Earthfile writes it and is not what containerd's
// image store stores: that keys on a fully-qualified name, and a layout
// annotated with the short form loaded into an image `docker images` listed -
// twice - `docker image inspect` denied existed, and `docker run` tried to
// fetch from a registry that had never heard of it. Running it by ID worked,
// which is what proved the image was right and only its name was wrong.
//
// The rules are docker's own and are older than any of this: a first component
// containing a dot or a colon is a registry host, anything else is a namespace
// on Docker Hub, a bare name is in the `library` namespace, and an absent tag
// is `latest`.
func FullReference(ref string) string {
	if ref == "" {
		return ref
	}

	name := ref

	// A digest names the image exactly and takes no tag.
	digest := ""
	if i := strings.Index(name, "@"); i >= 0 {
		name, digest = name[:i], name[i:]
	}

	host, rest, hasSlash := strings.Cut(name, "/")

	switch {
	case !hasSlash:
		// `alpine` and `alpine:3.22`: Docker Hub's library namespace.
		name = "docker.io/library/" + name
	case !strings.ContainsAny(host, ".:") && host != "localhost":
		// `myorg/app`: a namespace on Docker Hub rather than a registry host.
		name = "docker.io/" + host + "/" + rest
	}

	if digest != "" {
		return name + digest
	}

	// A colon after the last slash is a tag; one before it is a port.
	if i := strings.LastIndex(name, ":"); i < 0 || strings.Contains(name[i:], "/") {
		name += ":latest"
	}

	return name
}
