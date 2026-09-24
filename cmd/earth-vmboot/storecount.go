package main

import "strings"

// splitStore counts a store's real layers apart from the debris of writes that
// were killed.
//
// **The old figure was a directory count wearing the word "layer".** A layer is
// staged in `.<id>.partial-<n>` and renamed when it is whole, so a killed
// writer leaves that directory behind - and counting entries reported it as a
// layer. A store said "45,353 layer(s)" with 7M free, which is a number nobody
// could act on: it did not say how much of that was real and how much was
// rubble.
//
// Names that are neither are left out of both rather than guessed at. This
// count is read by somebody deciding whether to discard a build cache, and a
// stranger's file inflating either figure is worse than being absent from both.
func splitStore(names []string) (layers, debris int) {
	for _, name := range names {
		switch {
		case strings.HasPrefix(name, ".") && strings.Contains(name, ".partial-"):
			debris++

		case isLayerName(name):
			layers++
		}
	}

	return layers, debris
}

// isLayerName reports whether a name is a layer id as the store writes them:
// 64 lower-case hex characters, and nothing else.
func isLayerName(name string) bool {
	const idLen = 64

	if len(name) != idLen {
		return false
	}

	for _, c := range name {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}
