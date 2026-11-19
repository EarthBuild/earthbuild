#!/usr/bin/env bash
set -uex
set -o pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
earth="${earth:-${earthly:-${script_dir}/../../build/linux/amd64/earthly}}"
earth="$(realpath "$earth")"
cd "$script_dir"

# docker / podman
frontend="${frontend:-$(command -v docker || command -v podman)}"
test -n "$frontend" || { >&2 echo "Error: frontend is empty"; exit 1; }

install_a="earth-multi-a"
install_b="earth-multi-b"
container_a="${install_a}-buildkitd"
container_b="${install_b}-buildkitd"

cleanup() {
    "$frontend" rm -f "$container_a" "$container_b" >/dev/null 2>&1 || true
    "$frontend" volume rm -f "${install_a}-cache" "${install_b}-cache" >/dev/null 2>&1 || true
    rm -rf "$HOME/.${install_a}" "$HOME/.${install_b}"
}
trap cleanup EXIT

# Clean any leftovers from an interrupted previous run.
cleanup

run_instance() {
    local install_name="$1"
    EARTH_INSTALLATION_NAME="$install_name" "$earth" --ci +hello
}

echo "=== Start two buildkitd instances for different installation names ==="
run_instance "$install_a"
run_instance "$install_b"

echo "=== Verify both containers are running concurrently on distinct derived ports ==="
test "$("$frontend" inspect -f '{{.State.Running}}' "$container_a")" = "true"
test "$("$frontend" inspect -f '{{.State.Running}}' "$container_b")" = "true"

reg_port_a="$("$frontend" port "$container_a" 8371/tcp)"
reg_port_b="$("$frontend" port "$container_b" 8371/tcp)"
test -n "$reg_port_a"
test -n "$reg_port_b"
test "$reg_port_a" != "$reg_port_b"

# Podman publishes the TCP buildkitd port on the host (Docker uses docker-container://).
if bk_port_a="$("$frontend" port "$container_a" 8372/tcp 2>/dev/null)" && [ -n "$bk_port_a" ]; then
    bk_port_b="$("$frontend" port "$container_b" 8372/tcp)"
    test -n "$bk_port_b"
    test "$bk_port_a" != "$bk_port_b"
fi

id_a_before="$("$frontend" inspect -f '{{.Id}}' "$container_a")"
id_b_before="$("$frontend" inspect -f '{{.Id}}' "$container_b")"

echo "=== Re-run against both running instances (connectExisting path) ==="
run_instance "$install_a"
run_instance "$install_b"

echo "=== Verify neither container was recreated ==="
test "$("$frontend" inspect -f '{{.Id}}' "$container_a")" = "$id_a_before"
test "$("$frontend" inspect -f '{{.Id}}' "$container_b")" = "$id_b_before"

echo "multi-instance test passed"
