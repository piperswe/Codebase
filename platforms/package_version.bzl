"""Rules for exposing workspace-status values as package metadata files."""

def _package_status_value_impl(ctx):
    output = ctx.actions.declare_file(ctx.label.name + ".txt")
    ctx.actions.run_shell(
        inputs = [ctx.info_file],
        outputs = [output],
        arguments = [ctx.info_file.path, output.path, ctx.attr.key],
        command = """
set -eu
value="$(awk -v key="$3" '$1 == key {$1 = ""; sub(/^ /, ""); print; found = 1; exit} END {if (!found) exit 1}' "$1")"
printf '%s\n' "$value" > "$2"
""",
        mnemonic = "PackageStatusValue",
    )
    return [DefaultInfo(files = depset([output]))]

package_status_value = rule(
    implementation = _package_status_value_impl,
    attrs = {
        "key": attr.string(mandatory = True),
    },
)
