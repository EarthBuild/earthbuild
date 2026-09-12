#!/usr/bin/env bash
# Build a target, or answer from the record that nothing it reads has changed.
#
# Kept beside action.yml rather than inlined in a `run:` block, because a shell
# script embedded in YAML cannot be linted, cannot be run by hand to reproduce
# what CI did, and gets its quoting eaten twice.
set -euo pipefail

target=${EARTH_SKIP_TARGET:?no target}
record=${EARTH_SKIP_RECORD:?no record path}

mkdir -p "$record"
db="$record/db"

flags=(--engine=native --auto-skip --auto-skip-db-path "$db")

if [[ -n ${EARTH_SKIP_PLATFORM:-} ]]; then
  flags+=(--platform "$EARTH_SKIP_PLATFORM")
fi

# One NAME=value per line. Split on the FIRST `=` only, so a value may contain
# spaces and further `=`. A line with no `=` is an error and not a silent
# omission: an argument quietly dropped builds something other than what the
# workflow says, and the record would then be an honest record of the wrong
# build.
while IFS= read -r line; do
  [[ -z ${line//[[:space:]]/} ]] && continue
  [[ ${line#"${line%%[![:space:]]*}"} == \#* ]] && continue

  if [[ $line != *=* ]]; then
    echo "::error::build-args line is not NAME=value: ${line}" >&2
    exit 1
  fi

  name=${line%%=*}
  value=${line#*=}
  # The name is trimmed and the value is not: leading spaces in a value may be
  # meaningful, and a name with spaces is a typo rather than an argument.
  name=${name#"${name%%[![:space:]]*}"}
  name=${name%"${name##*[![:space:]]}"}

  flags+=(--build-arg "${name}=${value}")
done <<< "${EARTH_SKIP_ARGS:-}"

if [[ -n ${EARTH_SKIP_EXTRA:-} ]]; then
  # Deliberately word-split: this input is a command line, and the caller wrote
  # it as one.
  # shellcheck disable=SC2206
  extra=(${EARTH_SKIP_EXTRA})
  flags+=("${extra[@]}")
fi

out=$(mktemp)
trap 'rm -f "$out"' EXIT

echo "::group::earth ${flags[*]} ${target}"
set +e
earth "${flags[@]}" "$target" 2>&1 | tee "$out"
rc=${PIPESTATUS[0]}
set -e
echo "::endgroup::"

# **Matched on the engine's own words**, which is a coupling worth naming: there
# is no machine-readable signal for "this was skipped" and the exit code is 0
# either way. The sentences are asserted by the engine's tests, so they are at
# least not incidental - but a flag that said so directly would be better than
# this, and is the obvious next thing to add.
skipped=false
reason=

if grep -qF 'was built with these inputs before' "$out"; then
  skipped=true
fi

# The reason is the line under the refusal, indented. Plain assignment rather
# than `if why=$(grep ... | tail -1)`, which tests tail's status and so is taken
# whether grep matched or not.
why=$(grep -F 'will not be skipped' -A 1 "$out" | tail -1 || true)

if [[ -n $why && $why != *'will not be skipped'* ]]; then
  reason=${why#"${why%%[![:space:]]*}"}
fi

{
  echo "skipped=${skipped}"
  echo "reason=${reason}"
} >> "$GITHUB_OUTPUT"

if [[ $skipped == true ]]; then
  echo "### :fast_forward: \`${target}\` skipped" >> "$GITHUB_STEP_SUMMARY"
  echo "Nothing this target reads has changed since it was last built." \
    >> "$GITHUB_STEP_SUMMARY"
elif [[ -n $reason ]]; then
  # Surfaced rather than buried: a target that can never be skipped should say
  # so where somebody scanning the run will see it, or the flag looks broken.
  echo "::notice::${target} cannot be skipped: ${reason}"
fi

exit "$rc"
