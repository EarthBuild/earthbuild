#!/usr/bin/env bash
# Put the native engine back to a genuinely cold state, for benchmarking.
#
# **A fresh cache directory is not a cold engine.** `EARTH_CACHE_DIR` moves the
# image cache, the records and the index, but the *layer store* lives inside the
# sandbox - `StoreDir()` on macOS resolves beside the guest binary and is backed
# by a named `container` volume that outlives every build. So a benchmark run
# against a new temporary directory still finds its layers already unpacked, and
# reports a fetch of 0.2s that did nothing.
#
# This removes the sandbox and the volume behind it, which is the only way to
# make the next build pay what a first build pays.
#
# Only touches names beginning `earthbuild-`.
set -euo pipefail

dry=""
failed=0

if [ "${1:-}" = "--dry-run" ]; then
    dry="echo   would:"
fi

if ! command -v container >/dev/null 2>&1; then
    echo "the \`container\` CLI is not installed; nothing to reset" >&2
    exit 0
fi

# The listing is the whole of this script's input, so a backend that cannot
# answer means "removed nothing" reported as success. Start it rather than
# guessing - an apiserver that is already up treats this as a no-op.
if ! container ls -a --quiet >/dev/null 2>&1; then
    echo "== backend"
    if [ -n "$dry" ]; then
        echo "  would start the container service"
    else
        echo "  starting the container service"
        container system start >/dev/null 2>&1 || true
    fi
fi

echo "== sandboxes"
# `|| true` on the grep: no sandboxes is an ordinary state, and `pipefail`
# would otherwise read "nothing matched" as a failure and stop here.
# Fed by process substitution, not a pipe: a `while read` on the right of a
# pipe runs in a subshell, so `failed=1` set inside it never reaches the check
# at the end - and the script would report success having removed nothing.
#
# `--quiet` rather than `--format`: Apple's `container` does not take Go
# templates, and `--format '{{.Names}}'` is rejected with a usage message -
# which this loop then read as its list of sandboxes and found none in. The
# volumes below then refuse to go, because the sandboxes still hold them.
while read -r name; do
    if [ -n "$dry" ]; then
        echo "  would remove $name"
        continue
    fi

    # Reported rather than swallowed: a sandbox that would not go is the whole
    # reason the next run is not cold, and a reset that hides that is worse
    # than no reset at all.
    if out=$(container rm -f "$name" 2>&1); then
        echo "  removed $name"
    else
        echo "  COULD NOT remove $name: $out" >&2
        failed=1
    fi
done < <(container ls -a --quiet 2>/dev/null | grep '^earthbuild-' || true)

echo "== volumes"
while read -r vol; do
    if [ -n "$dry" ]; then
        echo "  would remove $vol"
        continue
    fi

    if out=$(container volume rm "$vol" 2>&1); then
        echo "  removed $vol"
    else
        echo "  COULD NOT remove $vol: $out" >&2
        failed=1
    fi
done < <(container volume ls 2>/dev/null | awk 'NR>1 {print $1}' | grep '^earthbuild-' || true)

# The host-side cache: image cache, records, index, profiles. Removed only when
# asked, because it is often the thing being measured rather than the thing in
# the way.
if [ "${EARTH_RESET_CACHE:-}" != "" ]; then
    cache="${EARTH_CACHE_DIR:-$HOME/.cache/earthbuild}"

    # **Checked before it is removed, every time.** This is an `rm -rf` on a
    # path that comes from the environment, so it is refused unless it is
    # absolute, several levels deep, and demonstrably an engine cache - a
    # directory holding `layers` or `imagecache`. An empty or unset variable
    # must never expand into a path that means something else.
    case "$cache" in
        "" | "/" | "$HOME" | "$HOME/") echo "refusing to remove $cache" >&2; exit 1 ;;
        /*) : ;;
        *) echo "refusing to remove a relative path: $cache" >&2; exit 1 ;;
    esac

    depth=$(printf '%s' "${cache#/}" | tr -cd '/' | wc -c)
    if [ "$depth" -lt 2 ]; then
        echo "refusing to remove $cache: too near the root to be a cache" >&2
        exit 1
    fi

    # **Nothing to remove is success, not a refusal.** A second reset finds the
    # cache already gone, and treating that as a failure made the script
    # non-idempotent - which stopped a benchmark between its two cold runs,
    # having reset correctly the first time.
    if [ ! -e "$cache" ]; then
        echo "== host cache"
        echo "  $cache is already gone"
        cache=""
    elif [ ! -d "$cache/layers" ] && [ ! -d "$cache/imagecache" ]; then
        echo "refusing to remove $cache: it exists but has no layers/ or" >&2
        echo "  imagecache/, so it is not an engine cache and this script will" >&2
        echo "  not guess" >&2
        exit 1
    fi
fi

if [ "${EARTH_RESET_CACHE:-}" != "" ] && [ -n "$cache" ]; then

    echo "== host cache"
    echo "  $cache"

    if [ -z "$dry" ]; then
        # **An unpacked image ships directories nothing may write to.**
        # `golang:1.26-alpine` has `usr/lib` at 0555 and `rm -rf` cannot empty
        # what it cannot write, so this failed on every layer with a read-only
        # directory - printing "Permission denied" and returning success.
        #
        # The effect was a reset that did not reset: the next build found its
        # layers where it left them and was called cold. Every cold measurement
        # taken through this script was worth less than it looked.
        chmod -R u+rwX "$cache" 2>/dev/null || true
    fi

    $dry rm -rf "$cache"

    # **Checked, because the failure above was silent.** A cache that is still
    # there after being removed is the one thing this script exists to prevent,
    # and it must not be reported as done.
    if [ -z "$dry" ] && [ -e "$cache" ]; then
        echo "  COULD NOT remove $cache - it is still there" >&2
        failed=1
    fi
fi

# **A wedged VM does not answer `container rm`.** Its runtime process stops
# servicing XPC, every removal times out, and the sandbox stays - running,
# holding tens of thousands of open descriptors on the layer store. Thirty-two
# of them overflowed the *system-wide* file table on the development machine,
# after which no command on the machine could start at all.
#
# Stopping the service reaps the runtime processes wholesale, which is the only
# thing that shifts one. Done once, only when the polite removals left
# something behind, and followed by a second pass over what remains.
if [ "$failed" -ne 0 ] && [ -z "$dry" ]; then
    echo "== wedged sandboxes"
    echo "  stopping the container service to reap them"
    container system stop >/dev/null 2>&1 || true
    container system start >/dev/null 2>&1 || true

    failed=0
    while read -r name; do
        if out=$(container rm -f "$name" 2>&1); then
            echo "  removed $name"
        else
            echo "  COULD NOT remove $name: $out" >&2
            failed=1
        fi
    done < <(container ls -a --quiet 2>/dev/null | grep '^earthbuild-' || true)

    while read -r vol; do
        if out=$(container volume rm "$vol" 2>&1); then
            echo "  removed volume $vol"
        else
            echo "  COULD NOT remove volume $vol: $out" >&2
            failed=1
        fi
    done < <(container volume ls 2>/dev/null | awk 'NR>1 {print $1}' | grep '^earthbuild-' || true)
fi

if [ "$failed" -ne 0 ]; then
    echo "NOT fully reset - see the errors above; the next build will not be cold" >&2
    exit 1
fi

echo "done - the next build pays what a first build pays"
