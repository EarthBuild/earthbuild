#!/usr/bin/env sh
# Generate a BUILD file with N independent genrules.
#
# **Independent on purpose.** A chain would measure the critical path and a
# fan-in would measure the scheduler; N actions that cannot see each other
# measure the per-action cost, which is the thing being compared.
#
# Each does a little real work - a loop and a checksum - so that an action is
# not purely overhead, and every one differs so nothing can be deduplicated or
# served from a cache that was supposed to be empty.
set -eu
n="${1:?usage: gen.sh N}"
: > BUILD
i=0
while [ "$i" -lt "$n" ]; do
  cat >> BUILD <<RULE
genrule(
    name = "t$i",
    outs = ["out/t$i.txt"],
    cmd = "i=0; s=0; while [ \$\$i -lt 200 ]; do s=\$\$((s+i+$i)); i=\$\$((i+1)); done; echo \$\$s > \$@",
)
RULE
  i=$((i+1))
done
echo "generated $n genrules"
