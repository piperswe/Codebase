"""Configuration isolation for architecture-specific package actions."""

def _package_arch_transition_impl(settings, attr):
    return {
        "//platforms:package_arch": attr.arch,
    }

_package_arch_transition = transition(
    implementation = _package_arch_transition_impl,
    inputs = [],
    outputs = ["//platforms:package_arch"],
)

def _package_artifact_impl(ctx):
    package = ctx.attr.package[0][DefaultInfo].files.to_list()
    if len(package) != 1:
        fail("package target must produce exactly one file")

    output = ctx.actions.declare_file(ctx.attr.out)
    ctx.actions.symlink(
        output = output,
        target_file = package[0],
    )
    return [
        DefaultInfo(
            files = depset([output]),
        ),
    ]

package_artifact = rule(
    implementation = _package_artifact_impl,
    attrs = {
        "arch": attr.string(
            mandatory = True,
            values = ["amd64", "arm64"],
        ),
        "package": attr.label(
            mandatory = True,
            cfg = _package_arch_transition,
        ),
        "out": attr.string(mandatory = True),
        "_allowlist_function_transition": attr.label(
            default = "@bazel_tools//tools/allowlists/function_transition_allowlist",
        ),
    },
)
