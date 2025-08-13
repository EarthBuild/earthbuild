#!/bin/bash
set -xeuo pipefail

cpubrand=$(sysctl -n machdep.cpu.brand_string)
echo "macOS test running on $cpubrand"

earthbuild="earthbuild"
if ! command -v "$earthbuild"; then
    earthbuild="earth"
fi

brew upgrade earthbuild

"$earthbuild" config global.disable_analytics true

"$earthbuild" --version

"$earthbuild" github.com/earthbuild/earthbuild/examples/go:main+docker
