#!/bin/sh
# golangci-lint.sh - install (if needed) and run golangci-lint at the pinned version.
# Used by both prek hooks and the +lint Earthfile target so the version is
# defined in exactly one place.
#
# Usage: golangci-lint.sh [extra golangci-lint args...]
#   e.g. golangci-lint.sh --fix

# renovate: datasource=github-releases packageName=golangci/golangci-lint
GOLANGCI_LINT_VERSION="2.12.2"

set -eu

# Install if the binary is missing or is the wrong version.
if ! command -v golangci-lint > /dev/null 2>&1 || \
   ! golangci-lint version 2>&1 | grep -qF "v${GOLANGCI_LINT_VERSION}"; then
    echo "installing golangci-lint v${GOLANGCI_LINT_VERSION}..."
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
        | sh -s -- -b "$(go env GOPATH)/bin" "v${GOLANGCI_LINT_VERSION}"
fi

golangci-lint run "$@"
