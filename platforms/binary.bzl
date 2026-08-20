def _platform_transition_impl(settings, attr):
    return {
        "//command_line_option:platforms": str(attr.platform),
    }

_platform_transition = transition(
    implementation = _platform_transition_impl,
    inputs = [],
    outputs = ["//command_line_option:platforms"],
)

def _platform_binary_impl(ctx):
    return [
        DefaultInfo(
            files = ctx.attr.binary[0][DefaultInfo].files,
        ),
    ]

platform_binary = rule(
    implementation = _platform_binary_impl,
    attrs = {
        "binary": attr.label(
            mandatory = True,
            cfg = _platform_transition,
        ),
        "platform": attr.label(
            mandatory = True,
        ),
        "_allowlist_function_transition": attr.label(
            default = "@bazel_tools//tools/allowlists/function_transition_allowlist",
        ),
    },
)
