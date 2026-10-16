package guest

// storeInVMByDefault is true where the sandbox is a virtual machine.
//
// **The shared mount is the slow one and the wrong one.** Every metadata
// operation on it crosses the VM boundary - 0.31ms per file a step opens - while
// the guest's own device is a filesystem in its kernel. Measured end to end on a
// cold build of a 14,541-file image, three pairs, the same layout either side:
// 61.0s/52.1s/45.8s on the shared mount against 44.5s/39.9s/34.5s on the device.
// About a third off, every time.
//
// It is also the only one that is *correct*. macOS is case-insensitive by
// default - APFS ships in two flavours and the installer picks that one - so two
// files in a layer differing only in case collide on the way in. The guest's
// volume is ext4: `container volume create` then `touch Foo.txt` leaves
// `foo.txt` absent. The engine used to answer this with five lines of advice
// about `hdiutil create`; the store simply not being there is a better answer.
//
// What was thought to be the cost is not one. A volume outlives the container
// that used it - written by one, read back by another after the first was
// removed - so the cache does not die with the sandbox.
//
// `EARTH_STORE_IN_VM=0` puts it back on the shared mount, which is the way to
// answer "is this what broke my build" without rebuilding the engine.
const storeInVMByDefault = true
