#!/usr/bin/env bash
# Run a command; on failure reset buildkitd state and try again.
#
# Why: earthly "Canceled" failures are non-deterministic - a different target
# each run, no signal received - so a second attempt usually succeeds. Genuine
# failures still fail: the last attempt's exit code propagates.
#
# The reset is the point. Dropping the buildkitd container alone is not enough;
# earthly's client-side state still references the dead session and the retry
# fails immediately with "no active sessions", so the cache volumes and
# ~/.earthly/buildkit go too.
#
# Usage:
#   earth-retry.sh [opts] -- CMD [ARG...]   run CMD directly; leading VAR=value
#                                           words are applied as env assignments
#   earth-retry.sh [opts] -c 'SHELL'        run a shell snippet (use for `&&`,
#                                           pipelines, or multi-line blocks)
#
# Options (all optional):
#   --attempts N     total attempts including the first  (default 3)
#   --binary NAME    container engine holding buildkitd  (default docker)
#   --sudo PREFIX    prefix for engine/rm commands, e.g. "sudo -E"
#   --sleep SECS     wait after resetting, before retrying  (default 0)
#   --bootstrap CMD  run after the reset, e.g. "earth bootstrap"
#   --log-tail N     buildkitd log lines to dump before the reset (default 300)

set -uo pipefail

attempts=3
binary=docker
sudo_prefix=""
sleep_secs=0
bootstrap=""
log_tail=300
snippet=""

die() {
  printf 'earth-retry: %s\n' "$1" >&2
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --attempts) attempts="${2:?--attempts needs a value}"; shift 2 ;;
    --binary) binary="${2:?--binary needs a value}"; shift 2 ;;
    --sudo) sudo_prefix="${2-}"; shift 2 ;;
    --sleep) sleep_secs="${2:?--sleep needs a value}"; shift 2 ;;
    --bootstrap) bootstrap="${2-}"; shift 2 ;;
    --log-tail) log_tail="${2:?--log-tail needs a value}"; shift 2 ;;
    -c) snippet="${2:?-c needs a value}"; shift 2 ;;
    --) shift; break ;;
    -*) die "unknown option $1" ;;
    *) break ;;
  esac
done

if [ -n "$snippet" ] && [ $# -gt 0 ]; then
  die "use either -c or a trailing command, not both"
fi
if [ -z "$snippet" ] && [ $# -eq 0 ]; then
  die "nothing to run; pass -c 'SHELL' or -- CMD [ARG...]"
fi

# Both forms become one arg vector, so the attempt loop has a single code path.
if [ -n "$snippet" ]; then
  set -- bash -c "$snippet"
fi

reset_buildkit() {
  # **A native run has no buildkitd and no container engine here.** `--binary`
  # names what holds buildkitd, and on the native suite it names the engine
  # itself - so every command below would be `native logs ...`, exit 127, and be
  # swallowed by the `|| true` that is there for a different reason. Swallowed
  # noise is still noise, and a reset that cannot reset anything should say so
  # once rather than fail four times quietly.
  if ! command -v "$binary" >/dev/null 2>&1; then
    echo "no $binary on this machine, so there is no buildkitd to reset"
    if [ "$sleep_secs" -gt 0 ] 2>/dev/null; then
      sleep "$sleep_secs"
    fi
    return 0
  fi

  if [ "$log_tail" -gt 0 ] 2>/dev/null; then
    # Before the reset, which destroys it. Without this an attempt-1
    # session-loss failure is undiagnosable.
    echo "::group::buildkitd logs from failed attempt $1"
    $sudo_prefix "$binary" logs earthly-buildkitd 2>&1 | tail -n "$log_tail" || true
    echo "::endgroup::"
  fi
  $sudo_prefix "$binary" rm -fv earthly-buildkitd earthly-dev-buildkitd 2>/dev/null || true
  $sudo_prefix "$binary" volume rm earthly-cache earthly-dev-cache 2>/dev/null || true
  $sudo_prefix rm -rf ~/.earthly/buildkit ~/.earthly-dev/buildkit 2>/dev/null || true
  if [ "$sleep_secs" -gt 0 ] 2>/dev/null; then
    sleep "$sleep_secs"
  fi
  if [ -n "$bootstrap" ]; then
    bash -c "$bootstrap" 2>/dev/null || true
  fi
}

attempt=1
while [ "$attempt" -le "$attempts" ]; do
  echo "::group::attempt $attempt of $attempts"
  # Via env(1), not plain "$@": bash honours `VAR=value` prefixes only on literal
  # command words, so `-- frontend=docker ./test.sh` would try to exec
  # `frontend=docker` and exit 127 on every attempt. env execs in place, so this
  # costs no process and keeps 126/127 exit semantics. No `set -e` either: a -c
  # snippet's exit code is its last command's, matching the loops this replaces.
  env -- "$@"
  rc=$?
  echo "::endgroup::"

  [ "$rc" -eq 0 ] && exit 0
  [ "$attempt" -eq "$attempts" ] && exit "$rc"

  echo "Attempt $attempt exited $rc; resetting buildkitd state and retrying."
  reset_buildkit "$attempt"
  attempt=$((attempt + 1))
done
