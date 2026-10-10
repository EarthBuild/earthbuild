// Package fstime sets filesystem times that the standard library cannot.
//
// One function, in its own package, because three packages need it and no two
// of them may import each other: the image unpacker, the layer restorer and the
// guest's copier all have to stamp a symlink, and `engine/image` cannot reach
// `engine/layer` without a cycle through `engine/ir`. It had been written three
// times instead, which is how the same defect was fixed three times and shipped
// a fourth (E546).
package fstime
