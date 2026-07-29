#!/bin/bash
# Usage: assert-default-buildkit-image.sh <expected-image> <command> [args...]
#
# Asserts that the earthly CLI invoked via "<command> [args...]" was compiled
# with <expected-image> as its default --buildkit-image. This is the image the
# CLI pulls when it needs to start a local buildkitd, so it must point at an
# image the release pipeline actually publishes (see issue #715).
#
# The staging release workflow runs this against both the release binaries and
# the published docker image (via "docker run"), so the two stay in sync.
set -euo pipefail

expected_image="$1"
shift

echo "Smoke testing: $* (expected default buildkit image: ${expected_image})"
"$@" --version || true

help_output="$("$@" --help 2>&1 || true)"

# Match on the flag name only. The help placeholder token varies by
# urfave/cli version ("--buildkit-image string" on v3, "value" on v2),
# so grepping for a specific placeholder is brittle.
actual_line="$(printf '%s\n' "${help_output}" | grep -- '--buildkit-image' || true)"

if [ -z "${actual_line}" ]; then
  echo "::error::Could not find the --buildkit-image flag in --help output."
  echo "Full --help output follows for debugging:"
  printf '%s\n' "${help_output}"
  exit 1
fi

echo "Found flag line:${actual_line}"

if [[ "${actual_line}" != *"(default: \"${expected_image}\")"* ]]; then
  echo "::error::Default buildkit image is incorrect."
  echo "  Expected default: ${expected_image}"
  echo "  Actual flag line:${actual_line}"
  exit 1
fi

echo "Smoke test passed: default buildkit image is ${expected_image}"
