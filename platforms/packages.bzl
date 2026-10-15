"""Helpers for building Linux packages for executables using nfpm."""

load("@bazel_skylib//rules:write_file.bzl", "write_file")
load("@rules_shell//shell:sh_test.bzl", "sh_test")
load(":nfpm.bzl", "nfpm_package")

_ARCHITECTURES = [
    ("amd64", "linux/amd64"),
    ("arm64", "linux/arm64/v8"),
]

# (packager, target group name, install test image, output extension)
_FORMATS = [
    ("deb", "debs", "debian:13.5-slim", "deb"),
    ("rpm", "rpms", "fedora:44", "rpm"),
    ("apk", "apks", "alpine:latest", "apk"),
    ("archlinux", "arch", "archlinux:base", "pkg.tar.zst"),
]

_HOMEPAGE = "https://codebase.bid"
_LICENSE = "AGPL-3.0-only"
_MAINTAINER = "Piper McCorkle <contact@piperswe.me>"

def package_file(src, destination, mode = "0644", config = False):
    """Describes a file and its installed location in a package.

    Args:
        src: label string; may contain `{arch}` and `{libc}` placeholders.
        destination: absolute install path.
        mode: octal file mode.
        config: mark as a config file (deb conffile, rpm %config(noreplace)).
    """
    if not destination.startswith("/"):
        fail("package destination must be absolute: {}".format(destination))
    return struct(
        config = config,
        destination = destination,
        mode = mode,
        src = src,
    )

def systemd_service_account(
        user,
        description,
        group = None,
        home = "/",
        shell = "/usr/sbin/nologin",
        state_directory = None,
        state_directory_mode = "0750"):
    """Describes a persistent account and optional state directory for a service."""
    if not home.startswith("/"):
        fail("systemd service account home must be absolute: {}".format(home))
    if state_directory != None and not state_directory.startswith("/"):
        fail("systemd state directory must be absolute: {}".format(state_directory))
    return struct(
        description = description,
        group = group or user,
        home = home,
        shell = shell,
        state_directory = state_directory,
        state_directory_mode = state_directory_mode,
        user = user,
    )

def _shell_quote(value):
    return "'{}'".format(value.replace("'", "'\"'\"'"))

def _systemd_quote(value):
    return '"{}"'.format(value.replace("\\", "\\\\").replace('"', '\\"'))

def _systemd_account_files(name, package_name, account):
    sysusers_name = name + "_sysusers"
    sysusers_path = "/usr/lib/sysusers.d/{}.conf".format(package_name)
    sysusers_content = []
    user_id = "-"
    if account.group != account.user:
        sysusers_content.append("g {} -".format(account.group))
        user_id = "-:{}".format(account.group)
    sysusers_content.append("u {} {} {} {} {}".format(
        account.user,
        user_id,
        _systemd_quote(account.description),
        account.home,
        account.shell,
    ))
    write_file(
        name = sysusers_name,
        out = sysusers_name + ".conf",
        content = sysusers_content,
    )

    files = [
        package_file(
            src = ":" + sysusers_name,
            destination = sysusers_path,
        ),
    ]
    tmpfiles_path = None
    if account.state_directory != None:
        tmpfiles_name = name + "_tmpfiles"
        tmpfiles_path = "/usr/lib/tmpfiles.d/{}.conf".format(package_name)
        write_file(
            name = tmpfiles_name,
            out = tmpfiles_name + ".conf",
            content = ["d {} {} {} {} - -".format(
                account.state_directory,
                account.state_directory_mode,
                account.user,
                account.group,
            )],
        )
        files.append(package_file(
            src = ":" + tmpfiles_name,
            destination = tmpfiles_path,
        ))

    return struct(
        files = files,
        sysusers_path = sysusers_path,
        tmpfiles_path = tmpfiles_path,
    )

def _systemd_package_scripts(name, service, account_files):
    """Generates format-agnostic maintainer scripts for the systemd setup."""
    postinstall = name + "_postinstall"
    postinstall_content = [
        "#!/bin/sh",
        "set -e",
    ]
    if account_files != None:
        # Alpine ships these as sd-sysusers/sd-tmpfiles (sd-tools package).
        postinstall_content.extend([
            "sysusers=systemd-sysusers",
            "command -v \"$sysusers\" >/dev/null 2>&1 || sysusers=sd-sysusers",
            "\"$sysusers\" {}".format(_shell_quote(account_files.sysusers_path)),
        ])
        if account_files.tmpfiles_path != None:
            postinstall_content.extend([
                "tmpfiles=systemd-tmpfiles",
                "command -v \"$tmpfiles\" >/dev/null 2>&1 || tmpfiles=sd-tmpfiles",
                "\"$tmpfiles\" --create {}".format(_shell_quote(account_files.tmpfiles_path)),
            ])
    postinstall_content.extend([
        "if command -v systemctl >/dev/null 2>&1; then",
        "  systemctl daemon-reload >/dev/null 2>&1 || true",
        "fi",
        "exit 0",
    ])
    write_file(
        name = postinstall,
        out = postinstall + ".sh",
        content = postinstall_content,
        is_executable = True,
    )

    preremove = name + "_preremove"
    preremove_content = [
        "#!/bin/sh",
        "set -e",
    ]
    if service != None:
        preremove_content.extend([
            "if test \"${1:-}\" = \"remove\" && command -v systemctl >/dev/null 2>&1; then",
            "  systemctl disable --now {} >/dev/null 2>&1 || true".format(_shell_quote(service)),
            "fi",
        ])
    preremove_content.append("exit 0")
    write_file(
        name = preremove,
        out = preremove + ".sh",
        content = preremove_content,
        is_executable = True,
    )

    postremove = name + "_postremove"
    write_file(
        name = postremove,
        out = postremove + ".sh",
        content = [
            "#!/bin/sh",
            "set -e",
            "if command -v systemctl >/dev/null 2>&1; then",
            "  systemctl daemon-reload >/dev/null 2>&1 || true",
            "fi",
            "exit 0",
        ],
        is_executable = True,
    )

    return {
        ":" + postinstall: "postinstall",
        ":" + preremove: "preremove",
        ":" + postremove: "postremove",
    }

def _package_install_test(
        name,
        package,
        package_format,
        package_name,
        docker_platform,
        image,
        files,
        install_test_command,
        systemd_account,
        systemd_service,
        visibility):
    sh_test(
        name = name,
        srcs = ["//platforms:package_install_test.sh"],
        args = [
            package_format,
            docker_platform,
            image,
            "$(rlocationpath {})".format(package),
            "$(rlocationpath //platforms:package_install_container.sh)",
            package_name,
            systemd_service or "-",
            systemd_account.user if systemd_account != None else "-",
            systemd_account.group if systemd_account != None else "-",
            systemd_account.home if systemd_account != None else "-",
            systemd_account.shell if systemd_account != None else "-",
            systemd_account.state_directory if systemd_account != None and systemd_account.state_directory != None else "-",
            systemd_account.state_directory_mode if systemd_account != None and systemd_account.state_directory != None else "-",
            str(len(files)),
        ] + [file.destination for file in files] + install_test_command,
        data = [
            package,
            "//platforms:package_install_container.sh",
        ],
        size = "large",
        tags = [
            "local",
            "no-sandbox",
            "requires-docker",
            "requires-network",
        ],
        timeout = "long",
        use_bash_launcher = True,
        visibility = visibility,
    )

def linux_packages(
        name,
        package_name,
        description,
        files,
        depends = {},
        install_test_command = [],
        systemd_account = None,
        systemd_service = None,
        visibility = None):
    """Creates amd64/arm64 deb, rpm, apk, and archlinux packages from file mappings.

    Args:
        name: target name; per-package targets are `<name>_<format>_<arch>`.
        package_name: upstream package name used in package metadata.
        description: package description.
        files: list of `package_file` structs. `src` labels may contain
            `{arch}` and `{libc}` placeholders (musl only for apk).
        depends: format -> list of package dependencies.
        install_test_command: command run in the install test after install.
        systemd_account: `systemd_service_account` struct or None.
        systemd_service: systemd unit name enabled in maintainer scripts.
        visibility: visibility of the generated targets.
    """
    package_files = files
    account_files = None
    if systemd_account != None:
        account_files = _systemd_account_files(name, package_name, systemd_account)
        package_files = files + account_files.files

    scripts = {}
    if systemd_service != None or account_files != None:
        scripts = _systemd_package_scripts(name, systemd_service, account_files)

    all_targets = []
    install_tests = []
    format_targets = {}
    for _, group, _, _ in _FORMATS:
        format_targets[group] = []

    for packager, group, image, extension in _FORMATS:
        for arch, docker_platform in _ARCHITECTURES:
            # Alpine packages need musl-linked binaries; everyone else gets glibc.
            libc = "musl" if packager == "apk" else "gnu"
            pkg_target = "{}_{}_{}".format(name, packager, arch)
            nfpm_package(
                name = pkg_target,
                out = "{}_{}.{}".format(package_name, arch, extension),
                packager = packager,
                arch = arch,
                package_name = package_name,
                description = description,
                homepage = _HOMEPAGE,
                maintainer = _MAINTAINER,
                license = _LICENSE,
                depends = depends.get(packager, []),
                files = {
                    file.src.format(arch = arch, libc = libc): file.destination
                    for file in package_files
                },
                file_modes = {
                    file.destination: file.mode
                    for file in package_files
                },
                config_destinations = [
                    file.destination
                    for file in package_files
                    if file.config
                ],
                scripts = scripts,
                version_file = "//platforms:package_version",
                tags = ["manual"],
            )
            format_targets[group].append(":" + pkg_target)
            all_targets.append(":" + pkg_target)

            test_target = "{}_{}_{}_install_test".format(name, packager, arch)
            if packager == "archlinux" and arch != "amd64":
                # There is no arm64 archlinux docker image; the amd64 test
                # (emulated) still covers the packager end-to-end.
                continue
            _package_install_test(
                name = test_target,
                package = ":" + pkg_target,
                package_format = packager,
                package_name = package_name,
                docker_platform = docker_platform,
                image = image,
                files = package_files,
                install_test_command = install_test_command,
                systemd_account = systemd_account,
                systemd_service = systemd_service,
                visibility = visibility,
            )
            install_tests.append(":" + test_target)

    for _, group, _, _ in _FORMATS:
        native.filegroup(
            name = "{}_{}".format(name, group),
            srcs = format_targets[group],
            visibility = visibility,
        )
    native.filegroup(
        name = name + "_packages",
        srcs = all_targets,
        visibility = visibility,
    )
    native.test_suite(
        name = name + "_install_tests",
        tests = install_tests,
        visibility = visibility,
    )

def linux_binary_packages(
        name,
        package_name,
        binary,
        executable_name,
        description,
        extra_files = [],
        depends = {},
        install_test_command = [],
        systemd_account = None,
        systemd_service = None,
        visibility = None):
    """Packages an architecture-specific Linux binary under /usr/bin.

    `binary` is a label string containing `{arch}` and `{libc}` placeholders
    pointing at per-arch, per-libc `platform_binary` targets.
    """
    linux_packages(
        name = name,
        package_name = package_name,
        description = description,
        files = [
            package_file(
                src = binary,
                destination = "/usr/bin/" + executable_name,
                mode = "0755",
            ),
        ] + extra_files,
        depends = depends,
        install_test_command = install_test_command,
        systemd_account = systemd_account,
        systemd_service = systemd_service,
        visibility = visibility,
    )
