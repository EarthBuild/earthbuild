#!/bin/bash
set -xeuo pipefail

earthbuild="earthbuild"
if ! command -v "$earthbuild"; then
    earthbuild="earth"
fi

"$earthbuild" config global.disable_analytics true

"$earthbuild" --version

"$earthbuild" github.com/EarthBuild/earthbuild-example-scala/simple:main+test
"$earthbuild" github.com/EarthBuild/earthbuild-example-scala/simple:main+docker
