#!/usr/bin/env bash
# Add swap, then reclaim unused toolchains, on a CI runner.
#
# Why: the 16 GiB GitHub runners silently OOM-kill buildkit-runc children.
# That surfaces as earthly "Canceled" mid-build and runc "file already closed"
# - the kill itself never reaches the job log.
#
# Where: the swapfile lands on the largest suitable non-root disk. On GitHub's
# ubuntu images that is /mnt, the ~70 GiB Azure ephemeral disk, which keeps a
# 12 GiB file off / where docker and buildkit layers need every spare byte.
# Only if no such disk exists does it fall back to /.
#
# Slow (mkswap, plus ~25 GiB of toolchain deletion), so CI calls it with
# --detach: it re-execs itself detached, appends to a log, and writes its exit
# code to a sentinel when finished. Nothing waits on it - swap that arrives a
# minute into the build is still swap. Use --wait if you need the barrier.
#
# Usage:
#   add-swap.sh                 run in the foreground
#   add-swap.sh --detach        run in the background, print the log path
#   add-swap.sh --wait [SECS]   block until a detached run finishes (default 300)
#   add-swap.sh --dry-run       report the disk it would use, change nothing
#
# Env: SWAP_SIZE (default 12G), SWAP_LOG (default $RUNNER_TEMP/add-swap.log).

set -euo pipefail

SWAP_SIZE="${SWAP_SIZE:-12G}"
SWAP_LOG="${SWAP_LOG:-${RUNNER_TEMP:-/tmp}/add-swap.log}"
SWAP_DONE="${SWAP_LOG}.done"

# Candidate mount points, best first. / is deliberately last. Kept as a string
# so --detach can export it - bash cannot export an array.
SWAP_DIRS="${SWAP_DIRS:-/mnt /datadisk /}"
read -r -a swap_dirs <<<"$SWAP_DIRS"
# Slack left free on the chosen filesystem after the swapfile, in MiB.
SWAP_SLACK_MIB="${SWAP_SLACK_MIB:-4096}"

# Preinstalled toolchains no EarthBuild target uses; ~25 GiB on ubuntu-24.04.
RECLAIM_PATHS=(
  /usr/share/dotnet
  /usr/local/lib/android
  /opt/ghc
  /usr/local/.ghcup
  /opt/hostedtoolcache/CodeQL
)

SUDO=(sudo)
if [[ "$(id -u)" -eq 0 ]]; then
  SUDO=()
fi

script_path() {
  local dir
  dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
  printf '%s/%s\n' "$dir" "$(basename -- "${BASH_SOURCE[0]}")"
}

# stderr, not stdout: pick_swap_dir and to_mib are consumed by $(...), and a
# diagnostic on stdout would be captured as part of their return value.
log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# "12G" / "512M" / "2048" -> MiB. A bare number is already MiB.
to_mib() {
  local spec="$1"
  case "$spec" in
    *[Gg]) printf '%s\n' $(( ${spec%[Gg]} * 1024 )) ;;
    *[Mm]) printf '%s\n' "${spec%[Mm]}" ;;
    *[0-9]) printf '%s\n' "$spec" ;;
    *) log "ERROR: bad SWAP_SIZE '$spec'"; return 1 ;;
  esac
}

avail_mib() { df -Pk "$1" | awk 'NR==2 {print int($4/1024)}'; }
fstype_of() { findmnt -no FSTYPE --target "$1" 2>/dev/null || echo unknown; }
source_of() { findmnt -no SOURCE --target "$1" 2>/dev/null || echo unknown; }

# Echo the best directory to hold a swapfile of $1 MiB, or nothing if none fits.
# A candidate that shares a device with / is not a separate disk, so it is only
# worth using when / itself is the fallback.
pick_swap_dir() {
  local need_mib="$1" root_src dir src fs avail
  root_src="$(source_of /)"
  for dir in "${swap_dirs[@]}"; do
    [[ -d "$dir" ]] || continue
    src="$(source_of "$dir")"
    fs="$(fstype_of "$dir")"
    avail="$(avail_mib "$dir")"
    if [[ "$dir" != / && "$src" == "$root_src" ]]; then
      log "skip $dir: same device as / ($src)"
      continue
    fi
    # Swapfiles need real extents; tmpfs/overlay/zfs/btrfs cannot serve them.
    case "$fs" in
      ext2 | ext3 | ext4 | xfs) ;;
      *)
        log "skip $dir: fstype $fs cannot host a swapfile"
        continue
        ;;
    esac
    if (( avail < need_mib + SWAP_SLACK_MIB )); then
      log "skip $dir: ${avail}MiB free, need $(( need_mib + SWAP_SLACK_MIB ))MiB"
      continue
    fi
    log "chose $dir: $fs on $src, ${avail}MiB free"
    printf '%s\n' "$dir"
    return 0
  done
  return 1
}

# fallocate is instant but leaves unwritten extents that mkswap rejects on some
# filesystems; dd always works and costs a minute. Try the cheap path first.
allocate() {
  local path="$1" mib="$2"
  if "${SUDO[@]}" fallocate -l "${mib}M" "$path" 2>/dev/null &&
    "${SUDO[@]}" chmod 600 "$path" &&
    "${SUDO[@]}" mkswap "$path" >/dev/null 2>&1; then
    return 0
  fi
  log "fallocate path unusable, rewriting ${path} with dd"
  "${SUDO[@]}" rm -f "$path"
  "${SUDO[@]}" dd if=/dev/zero of="$path" bs=1M count="$mib" status=none
  "${SUDO[@]}" chmod 600 "$path"
  "${SUDO[@]}" mkswap "$path" >/dev/null
}

add_swap() {
  local mib dir path
  mib="$(to_mib "$SWAP_SIZE")"
  if ! dir="$(pick_swap_dir "$mib")"; then
    log "ERROR: no filesystem with room for a ${mib}MiB swapfile"
    df -h
    return 1
  fi
  path="${dir%/}/extra-swapfile"
  if swapon --show=NAME --noheadings | grep -qxF "$path"; then
    log "swapfile $path already active"
    return 0
  fi
  "${SUDO[@]}" rm -f "$path"
  allocate "$path" "$mib"
  "${SUDO[@]}" swapon "$path"
  log "swap added at $path"
  swapon --show
  free -h
}

reclaim_disk() {
  local before after
  before="$(avail_mib /)"
  "${SUDO[@]}" rm -rf "${RECLAIM_PATHS[@]}"
  after="$(avail_mib /)"
  log "reclaimed $(( after - before ))MiB on / (${after}MiB free)"
}

main() {
  local rc=0
  log "add-swap start: size=$SWAP_SIZE"
  add_swap || rc=$?
  # After swap, not before: swap is what stops the OOM killer, and it no longer
  # depends on the reclaim now that the file lives off /. Reclaim runs even if
  # swap failed - free root disk is worth having either way.
  reclaim_disk || rc=$?
  log "add-swap done (rc=$rc)"
  return "$rc"
}

case "${1:-}" in
  --detach)
    mkdir -p "$(dirname -- "$SWAP_LOG")"
    rm -f "$SWAP_DONE"
    # Export so the child resolves the same settings, not its own defaults.
    export SWAP_SIZE SWAP_LOG SWAP_DIRS SWAP_SLACK_MIB
    setsid nohup "$(script_path)" >>"$SWAP_LOG" 2>&1 </dev/null &
    log "add-swap detached as pid $!, logging to $SWAP_LOG"
    ;;
  --wait)
    timeout="${2:-300}"
    for _ in $(seq "$timeout"); do
      if [[ -f "$SWAP_DONE" ]]; then
        cat "$SWAP_LOG" 2>/dev/null || true
        exit "$(cat "$SWAP_DONE")"
      fi
      sleep 1
    done
    log "ERROR: detached add-swap did not finish within ${timeout}s"
    cat "$SWAP_LOG" 2>/dev/null || true
    exit 1
    ;;
  --dry-run)
    mib="$(to_mib "$SWAP_SIZE")"
    log "would allocate ${mib}MiB"
    if dir="$(pick_swap_dir "$mib")"; then
      log "would create ${dir%/}/extra-swapfile"
    else
      log "ERROR: no filesystem with room for a ${mib}MiB swapfile"
      exit 1
    fi
    log "would reclaim: ${RECLAIM_PATHS[*]}"
    ;;
  "")
    rc=0
    main || rc=$?
    printf '%s\n' "$rc" >"$SWAP_DONE"
    exit "$rc"
    ;;
  *)
    printf 'usage: %s [--detach | --wait [SECS] | --dry-run]\n' "$(basename -- "$0")" >&2
    exit 2
    ;;
esac
