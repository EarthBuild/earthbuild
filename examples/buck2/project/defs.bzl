"""The smallest buck2 project that produces an action.

No prelude rules and no toolchain: what is being tested is whether an action
reaches this engine and comes back, and a real toolchain would put a compiler's
problems between the question and the answer.
"""

def _platforms_impl(ctx):
    return [
        DefaultInfo(),
        ExecutionPlatformRegistrationInfo(
            platforms = [
                ExecutionPlatformInfo(
                    label = ctx.label.raw_target(),
                    configuration = ConfigurationInfo(constraints = {}, values = {}),
                    executor_config = CommandExecutorConfig(
                        # Remote only. Hybrid would let buck2 quietly run the
                        # action locally and report success, which is the one
                        # answer this experiment must not be able to give.
                        local_enabled = False,
                        remote_enabled = True,
                        use_limited_hybrid = True,
                        remote_execution_properties = {},
                        remote_execution_use_case = "buck2-default",
                    ),
                ),
            ],
        ),
    ]

platforms = rule(impl = _platforms_impl, attrs = {})

def _target_platform_impl(ctx):
    return [
        DefaultInfo(),
        PlatformInfo(
            label = str(ctx.label.raw_target()),
            configuration = ConfigurationInfo(constraints = {}, values = {}),
        ),
    ]

# Distinct from the execution platform above, which buck2 is strict about: one
# says what a target is built *for*, the other says where an action *runs*.
target_platform = rule(impl = _target_platform_impl, attrs = {})

def _greeting_impl(ctx):
    out = ctx.actions.declare_output("greeting.txt")

    ctx.actions.run(
        cmd_args(
            "/bin/sh",
            "-c",
            cmd_args(out.as_output(), format = "echo hello from an action > {}"),
        ),
        category = "greeting",
    )

    return [DefaultInfo(default_output = out)]

greeting = rule(impl = _greeting_impl, attrs = {})
