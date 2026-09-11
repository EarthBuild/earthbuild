package layer

// TakeManifested is Take, handing back the manifest for what it captured.
//
// **The walk has already read everything.** Capturing a layer reads every file
// to digest it and then discards the per-file digests; anything wanting them
// later - a peer authenticating a fragment, a copy deciding whether a file
// differs, a re-push asking what actually changed - pays for a second walk of a
// tree that has just been walked. Measured on a real 898 MB layer of 5725
// files: the walk costs 463 ms and the manifest it could have kept is 960 KB,
// or 0.107% of the layer. Across four real layers the manifest ran 96-168 bytes
// a file.
//
// The same argument `Capture.Marked` already makes one field over, where not
// writing down what the walk knew cost 7.1 seconds of an 8 second build in 36
// re-scans of trees already walked (E561).
//
// The bytes are identical to `Manifest`'s for the same tree, because they are
// the same encoding over the same entries - so nothing has two answers about
// what a layer contains.
func TakeManifested(root string) (Capture, []byte, error) {
	return TakeManifestedIn(root, IDMap{}, IDMap{})
}

// TakeManifestedIn is TakeManifested with ownership translated as TakeIn
// translates it.
//
// Both the capture and the manifest are translated, and they must be translated
// the same way: a manifest that disagreed with its layer about ownership would
// hash to a different layer and authenticate nothing (E313).
func TakeManifestedIn(root string, uids, gids IDMap) (Capture, []byte, error) {
	entries, size, err := walk(root)
	if err != nil {
		return Capture{}, nil, err
	}

	// `capture` sorts in place, and the manifest needs the same order - so it is
	// taken afterwards, over the slice capture has already put in order.
	c := capture(entries, size, uids, gids)

	return c, encodeEntries(entries, uids, gids), nil
}
