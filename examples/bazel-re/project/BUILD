# One genrule, which needs no toolchain to resolve.
#
# A real rule would put a compiler's problems between the question and the
# answer; this asks only whether an action reaches the engine and comes back.
genrule(
    name = "hello",
    outs = ["greeting.txt"],
    cmd = "echo hello from an action > $@",
)
