#!/usr/bin/env sh
# Time a ladder of bazel actions, remote against local.
#
# **A ladder rather than one build**, because the question has two answers: a
# fixed cost for turning remote execution on, and a cost per action. One build
# gives their sum and cannot separate them. Fitting a line over several N gives
# the per-action cost as the slope and the fixed cost as the intercept, which
# are the two numbers worth quoting.
#
# **Interleaved rather than batched.** A machine drifts - thermal, other load,
# page cache - and AAABBB attributes that drift to the arm that ran second.
# ABABAB cancels it.
#
# **Every run is checked**, because a build that fails early is faster than one
# that succeeds and would otherwise win. Each arm must report the number of
# actions it actually ran, in the mode it claimed.
set -eu

reps="${REPS:-3}"
sizes="${SIZES:-25 50 100}"

say() { printf '%s\n' "$*" >&2; }

# run <mode> <n> -> seconds, or exits non-zero having said why.
run() {
  mode="$1"; n="$2"
  shift 2

  # A cache nobody purged is a benchmark of a cache.
  rm -rf /root/.cache/bazel 2>/dev/null || true
  rm -rf "$HOME/.cache/bazel" 2>/dev/null || true

  # **A list, not a string.** The flags must word-split and a quoted string
  # would arrive as one argument; positional parameters say that deliberately
  # rather than relying on a split shellcheck is right to warn about.
  case "$mode" in
    remote)
      set -- --remote_executor=grpcs://127.0.0.1:8980 \
             --tls_certificate=/run/earthbuild/ca.pem \
             --strategy=Genrule=remote --spawn_strategy=remote \
             --noremote_accept_cached
      ;;
    # No executor at all, not merely a different strategy: an executor left
    # set would have this arm checking the remote cache, which is part of what
    # the other arm is being charged for.
    local)
      set -- --strategy=Genrule=local --spawn_strategy=local --remote_executor=
      ;;
    *) say "unknown mode $mode"; exit 2 ;;
  esac

  start=$(date +%s%N)
  out=$(bazel build //:all "$@" --remote_timeout=600 --noshow_progress 2>&1) || {
    say "FAIL $mode n=$n"; say "$out" | tail -5; exit 1
  }
  end=$(date +%s%N)

  # **Did it do the work, in the mode it says?** Bazel reports what it ran and
  # how; an arm that quietly fell back to the other strategy would otherwise be
  # compared against itself.
  line=$(printf '%s\n' "$out" | grep -E "^INFO: [0-9]+ processes:" || true)
  case "$mode:$line" in
    remote:*remote*) ;;
    local:*local*)   ;;
    *) say "FAIL $mode n=$n did not run $mode: ${line:-no process line}"; exit 1 ;;
  esac

  printf '%s' $(( (end - start) / 1000000 ))
}

printf 'mode\tn\trep\tms\n'

rep=1
while [ "$rep" -le "$reps" ]; do
  for n in $sizes; do
    sh "$(dirname "$0")/gen.sh" "$n" >/dev/null
    for mode in remote local; do
      ms=$(run "$mode" "$n")
      printf '%s\t%s\t%s\t%s\n' "$mode" "$n" "$rep" "$ms"
    done
  done
  rep=$((rep+1))
done
