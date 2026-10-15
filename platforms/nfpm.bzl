"""Hermetic package-building rule backed by nfpm."""

"""nfpm tool dependency, built from the go_deps module repository."""
_NFPM = "@com_github_goreleaser_nfpm_v2//cmd/nfpm"

_FILE_INFO = """    file_info:
      mode: {mode}
      owner: "root"
      group: "root"
"""

def _nfpm_package_impl(ctx):
    output = ctx.actions.declare_file(ctx.attr.out)

    # Bazel's sandbox presents inputs as symlinks, and nfpm packages symlink
    # srcs as symlink contents. Stage real copies (cp -L) and point the
    # config at the staged files.
    staged_files = []
    staged_paths = {}
    file_paths = []
    for dep, destination in sorted(
            ctx.attr.files.items(),
            key = lambda item: item[1]):
        dep_files = dep[DefaultInfo].files.to_list()
        if len(dep_files) != 1:
            fail("package file target must produce exactly one file: {}".format(dep))
        if dep_files[0].path not in staged_paths:
            staged_paths[dep_files[0].path] = "stage/{}".format(len(staged_files))
            staged_files.append(dep_files[0])
        file_paths.append((staged_paths[dep_files[0].path], destination))

    scripts = []
    for dep, hook in ctx.attr.scripts.items():
        dep_files = dep[DefaultInfo].files.to_list()
        if len(dep_files) != 1:
            fail("script target must produce exactly one file: {}".format(dep))
        scripts.append((hook, dep_files[0].path))

    config_lines = [
        "name: {}".format(json.encode(ctx.attr.package_name)),
        "arch: {}".format(json.encode(ctx.attr.arch)),
        "platform: \"linux\"",
        # Workspace-status version is injected via the action environment;
        # nfpm expands ${VAR} in this field.
        "version: \"${PACKAGE_VERSION}\"",
        "description: {}".format(json.encode(ctx.attr.description)),
        "homepage: {}".format(json.encode(ctx.attr.homepage)),
        "maintainer: {}".format(json.encode(ctx.attr.maintainer)),
        "license: {}".format(json.encode(ctx.attr.license)),
        "disable_globbing: true",
        "umask: 0o002",
    ]

    if ctx.attr.depends:
        config_lines.append("depends:")
        for dependency in sorted(ctx.attr.depends):
            config_lines.append("  - {}".format(json.encode(dependency)))

    config_lines.append("contents:")
    for src, dst in sorted(file_paths):
        config_lines.extend([
            "  - src: {}".format(json.encode(src)),
            "    dst: {}".format(json.encode(dst)),
        ])
        config_lines.append(_FILE_INFO.format(mode = ctx.attr.file_modes.get(dst, "0644")).rstrip("\n"))
        if dst in ctx.attr.config_destinations:
            # "config" maps to deb conffiles and rpm %config(noreplace).
            config_lines.append("    type: \"config\"")

    if scripts:
        config_lines.append("scripts:")
        for hook, path in sorted(scripts):
            config_lines.append("  {}: {}".format(hook, json.encode(path)))

    config = ctx.actions.declare_file(ctx.label.name + ".nfpm.yaml")
    ctx.actions.write(
        output = config,
        content = "\n".join(config_lines) + "\n",
    )

    version_file = ctx.attr.version_file[DefaultInfo].files.to_list()
    if len(version_file) != 1:
        fail("version_file must produce exactly one file")
    stage_commands = ["mkdir -p stage"]
    for index, file in enumerate(staged_files):
        stage_commands.append('cp -L "{path}" "stage/{index}"'.format(index = index, path = file.path))
    ctx.actions.run_shell(
        inputs = depset(
            [config, version_file[0]] + staged_files,
            transitive = [
                dep[DefaultInfo].files
                for dep in ctx.attr.scripts.keys()
            ],
        ),
        outputs = [output],
        tools = [ctx.executable._nfpm],
        arguments = [
            ctx.executable._nfpm.path,
            config.path,
            version_file[0].path,
            ctx.attr.packager,
            output.path,
        ],
        command = "\n".join([
            "set -eu",
            'export PACKAGE_VERSION="$(cat "$3")"',
        ] + stage_commands + [
            '"$1" package --config "$2" --packager "$4" --target "$5"',
        ]),
        mnemonic = "NfpmPackage",
        progress_message = "nfpm %s %s/%s" % (
            ctx.attr.packager,
            ctx.attr.package_name,
            ctx.attr.arch,
        ),
    )

    return [DefaultInfo(files = depset([output]))]

nfpm_package = rule(
    implementation = _nfpm_package_impl,
    attrs = {
        "arch": attr.string(
            doc = "Package architecture in Go nomenclature; nfpm translates per packager.",
            mandatory = True,
            values = ["amd64", "arm64"],
        ),
        "config_destinations": attr.string_list(
            doc = "Destinations of files that should be marked as config files.",
        ),
        "depends": attr.string_list(
            doc = "Package dependencies, already resolved for the target format.",
        ),
        "description": attr.string(mandatory = True),
        "file_modes": attr.string_dict(
            doc = "Destination -> octal file mode (as string, e.g. \"0755\").",
        ),
        "files": attr.label_keyed_string_dict(
            doc = "File target -> absolute install destination.",
            allow_files = True,
            mandatory = True,
        ),
        "homepage": attr.string(mandatory = True),
        "license": attr.string(mandatory = True),
        "maintainer": attr.string(mandatory = True),
        "out": attr.string(
            doc = "Output package file name.",
            mandatory = True,
        ),
        "package_name": attr.string(mandatory = True),
        "packager": attr.string(
            mandatory = True,
            values = ["deb", "rpm", "apk", "archlinux"],
        ),
        "scripts": attr.label_keyed_string_dict(
            doc = "Script file target -> maintainer script hook name.",
            allow_files = True,
        ),
        "version_file": attr.label(
            mandatory = True,
        ),
        "_nfpm": attr.label(
            default = _NFPM,
            cfg = "exec",
            executable = True,
        ),
    },
)
