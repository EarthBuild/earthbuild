#!/bin/bash
set -uxe
set -o pipefail

cd "$(dirname "$0")"
earthbuild=${earthbuild-"../../build/linux/amd64/earthbuild"}
host=$(hostname)

# so that we can test production/staging binaries
default_install_name="${default_install_name:-"earthbuild-dev"}"

mkdir -p ~/.${default_install_name}
touch ~/.${default_install_name}/config.yml
cp ~/.${default_install_name}/config.yml ~/.${default_install_name}/config.yml.bkup

function finish {
  mv ~/.${default_install_name}/config.yml.bkup ~/.${default_install_name}/config.yml
}
trap finish EXIT

echo "=== Test 1: TLS Enabled ==="
# FIXME bootstrap is failing with "open /home/runner/.${default_install_name}/certs/ca_cert.pem: permission denied", but generates them nonetheless.
"$earthbuild" --verbose --buildkit-host tcp://127.0.0.1:8372 bootstrap || (echo "ignoring bootstrap failure")

# bootstrapping should generate six pem files
test $(ls ~/.${default_install_name}/certs/*.pem | wc -l) = "6"

"$earthbuild" --no-cache --verbose --buildkit-host tcp://127.0.0.1:8372 +target 2>&1 | perl -pe 'BEGIN {$status=1} END {exit $status} $status=0 if /running under remote-buildkit test/;'

rm -rf ~/.${default_install_name}/certs

# force buildkit restart before next test
"$earthbuild" bootstrap || (echo "ignoring bootstrap failure")

echo "=== Test 2: TLS Enabled with different hostname ==="
# FIXME bootstrap is failing with "open /home/runner/.${default_install_name}/certs/ca_cert.pem: permission denied", but generates them nonetheless.
"$earthbuild" --verbose --buildkit-host tcp://127.0.0.1:8372 bootstrap --certs-hostname "$host" || (echo "ignoring bootstrap failure")

# bootstrapping should generate six pem files
test $(ls ~/.${default_install_name}/certs/*.pem | wc -l) = "6"

"$earthbuild" --no-cache --verbose --buildkit-host tcp://127.0.0.1:8372 +target 2>&1 | perl -pe 'BEGIN {$status=1} END {exit $status} $status=0 if /running under remote-buildkit test/;'

rm -rf ~/.${default_install_name}/certs

# force buildkit restart before next test
"$earthbuild" bootstrap || (echo "ignoring bootstrap failure")

echo "=== Test 3: TLS Disabled ==="
"$earthbuild" config global.tls_enabled false
# FIXME bootstrap is failing with "open /home/runner/.${default_install_name}/certs/ca_cert.pem: permission denied", but generates them nonetheless.
"$earthbuild" --verbose --buildkit-host tcp://127.0.0.1:8372 bootstrap || (echo "ignoring bootstrap failure")

# bootstrapping should not generate any pem files
test $(ls ~/.${default_install_name}/certs/*.pem | wc -l) = "0"

"$earthbuild" --no-cache --verbose --buildkit-host tcp://127.0.0.1:8372 +target 2>&1 | perl -pe 'BEGIN {$status=1} END {exit $status} $status=0 if /running under remote-buildkit test/;'
