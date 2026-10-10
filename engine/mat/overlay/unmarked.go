package overlay

// UnmarkedNote names the note that records a layer as carrying no whiteout
// markers, given the layer's directory.
//
// Beside the layer, never inside it: a layer is named by its content, and a file
// added to it is a layer that is no longer what it says it is.
//
// Exported because both ends write it. The guest writes one after scanning, and
// whoever places an image layer writes one without scanning at all - an image is
// flattened as it is unpacked and every `.wh.` entry is applied as a deletion
// there, so a placed image provably carries none. On a fresh VM, which is what
// CI has, the note in the store is the only one that exists (E531).
func UnmarkedNote(layerDir string) string { return layerDir + ".unmarked" }
