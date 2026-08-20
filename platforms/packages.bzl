"""Helpers for building Debian and RPM packages for Linux executables."""

load("@bazel_skylib//rules:write_file.bzl", "write_file")
load("@rules_pkg//pkg:mappings.bzl", "pkg_attributes", "pkg_filegroup", "pkg_files")
load("@rules_pkg//pkg:rpm_pfg.bzl", "pkg_rpm")
load("@rules_pkg//pkg/private/deb:deb.bzl", "pkg_deb")
load("@rules_pkg//pkg/private/tar:tar.bzl", "pkg_tar")
load("@rules_shell//shell:sh_test.bzl", "sh_test")
load("//platforms:package_transition.bzl", "package_artifact")

_ARCHITECTURES = [
    ("amd64", "amd64", "x86_64", "linux/amd64"),
    ("arm64", "arm64", "aarch64", "linux/arm64/v8"),
]

_DEBIAN_TEST_IMAGE = "debian:13.5-slim"
_FEDORA_TEST_IMAGE = "fedora:44"
_HOMEPAGE = "https://codeberg.org/pmc/Codebase"
_LICENSE = "AGPL-3.0-only"
_MAINTAINER = "Piper McCorkle <contact@piperswe.me>"

def package_file(src, destination, mode = "0644", config = False):
    """Describes a file and its installed location in a package."""
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

def _format_label(label, arch):
    return label.format(arch = arch)

def _systemd_quote(value):
    return '"{}"'.format(value.replace("\\", "\\\\").replace('"', '\\"'))

def _systemd_account_files(name, account):
    sysusers_name = name + "_sysusers"
    sysusers_path = "/usr/lib/sysusers.d/{}.conf".format(name)
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
        tmpfiles_path = "/usr/lib/tmpfiles.d/{}.conf".format(name)
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

def _systemd_debian_scripts(name, service, account_files):
    postinst = "{}_deb_postinst".format(name)
    prerm = "{}_deb_prerm".format(name)
    postrm = "{}_deb_postrm".format(name)

    postinst_content = [
        "#!/bin/sh",
        "set -e",
    ]
    if account_files != None:
        postinst_content.append("systemd-sysusers {}".format(_shell_quote(account_files.sysusers_path)))
        if account_files.tmpfiles_path != None:
            postinst_content.append("systemd-tmpfiles --create {}".format(_shell_quote(account_files.tmpfiles_path)))
    postinst_content.extend([
        "if command -v systemctl >/dev/null 2>&1; then",
        "  systemctl daemon-reload >/dev/null 2>&1 || true",
        "fi",
        "exit 0",
    ])
    write_file(
        name = postinst,
        out = postinst + ".sh",
        content = postinst_content,
        is_executable = True,
    )
    prerm_content = [
        "#!/bin/sh",
        "set -e",
    ]
    if service != None:
        prerm_content.extend([
            "if test \"${1:-}\" = \"remove\" && command -v systemctl >/dev/null 2>&1; then",
            "  systemctl disable --now {} >/dev/null 2>&1 || true".format(_shell_quote(service)),
            "fi",
        ])
    prerm_content.append("exit 0")
    write_file(
        name = prerm,
        out = prerm + ".sh",
        content = prerm_content,
        is_executable = True,
    )
    write_file(
        name = postrm,
        out = postrm + ".sh",
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

    return (":" + postinst, ":" + prerm, ":" + postrm)

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
        deb_depends = [],
        install_test_command = [],
        rpm_requires = [],
        systemd_account = None,
        systemd_service = None,
        visibility = None):
    """Creates amd64/arm64 Debian and RPM packages from file mappings."""
    deb_targets = []
    rpm_targets = []
    all_targets = []
    install_tests = []

    package_files = files
    account_files = None
    if systemd_account != None:
        account_files = _systemd_account_files(name, systemd_account)
        package_files = files + account_files.files

    postinst = None
    prerm = None
    postrm = None
    if systemd_service != None or account_files != None:
        postinst, prerm, postrm = _systemd_debian_scripts(name, systemd_service, account_files)

    for arch, deb_arch, rpm_arch, docker_platform in _ARCHITECTURES:
        mappings = []
        conffiles = []

        for index, file in enumerate(package_files):
            mapping_name = "{}_{}_file_{}".format(name, arch, index)
            src = _format_label(file.src, arch)
            attributes = {
                "mode": file.mode,
                "user": "root",
                "group": "root",
            }
            if file.config:
                attributes["rpm_filetag"] = "%config(noreplace)"
                conffiles.append(file.destination)

            pkg_files(
                name = mapping_name,
                srcs = [src],
                attributes = pkg_attributes(**attributes),
                prefix = "/",
                renames = {src: file.destination.lstrip("/")},
            )
            mappings.append(":" + mapping_name)

        contents_name = "{}_{}_contents".format(name, arch)
        pkg_filegroup(
            name = contents_name,
            srcs = mappings,
        )

        data_name = "{}_deb_{}_data".format(name, deb_arch)
        pkg_tar(
            name = data_name,
            out = data_name + ".tar.gz",
            extension = "tar.gz",
            srcs = [":" + contents_name],
        )

        deb_rule = "{}_deb_{}_pkg".format(name, deb_arch)
        deb_kwargs = {
            "name": deb_rule,
            "architecture": deb_arch,
            "conffiles": conffiles,
            "data": ":" + data_name,
            "depends": deb_depends,
            "description": description,
            "homepage": _HOMEPAGE,
            "license": _LICENSE,
            "maintainer": _MAINTAINER,
            "out": deb_rule + ".deb",
            "package": package_name,
            "package_file_name": "{}_{}.deb".format(package_name, deb_arch),
            "tags": ["manual"],
            "version_file": "//platforms:deb_package_version",
        }
        if postinst != None:
            deb_kwargs["postinst"] = postinst
            deb_kwargs["prerm"] = prerm
            deb_kwargs["postrm"] = postrm
        pkg_deb(**deb_kwargs)

        deb_target = "{}_deb_{}".format(name, deb_arch)
        native.filegroup(
            name = deb_target,
            srcs = [":" + deb_rule],
            output_group = "deb",
            visibility = visibility,
        )
        deb_targets.append(":" + deb_target)
        all_targets.append(":" + deb_target)

        deb_test = "{}_deb_{}_install_test".format(name, deb_arch)
        _package_install_test(
            name = deb_test,
            package = ":" + deb_target,
            package_format = "deb",
            package_name = package_name,
            docker_platform = docker_platform,
            image = _DEBIAN_TEST_IMAGE,
            files = package_files,
            install_test_command = install_test_command,
            systemd_account = systemd_account,
            systemd_service = systemd_service,
            visibility = visibility,
        )
        install_tests.append(":" + deb_test)

        rpm_rule = "{}_rpm_{}_pkg".format(name, rpm_arch)
        rpm_kwargs = {
            "name": rpm_rule,
            "architecture": rpm_arch,
            "defines": {
                "_target_cpu": rpm_arch,
                "_target_os": "linux",
                "__brp_strip": "%{nil}",
                "__brp_strip_lto": "%{nil}",
                "__brp_strip_static_archive": "%{nil}",
                "__brp_strip_comment_note": "%{nil}",
            },
            "description": description,
            "license": _LICENSE,
            "package_file_name": "{}_{}.rpm".format(package_name, rpm_arch),
            "package_name": package_name,
            "release_file": "//platforms:rpm_package_release",
            "requires": rpm_requires,
            "srcs": [":" + contents_name],
            "summary": description,
            "tags": ["manual"],
            "url": _HOMEPAGE,
            "version_file": "//platforms:rpm_package_version",
        }
        rpm_post_scriptlet = []
        if account_files != None:
            rpm_post_scriptlet.append("systemd-sysusers {}".format(_shell_quote(account_files.sysusers_path)))
            if account_files.tmpfiles_path != None:
                rpm_post_scriptlet.append("systemd-tmpfiles --create {}".format(_shell_quote(account_files.tmpfiles_path)))
        if systemd_service != None:
            quoted_service = _shell_quote(systemd_service)
            rpm_post_scriptlet.append("systemctl daemon-reload >/dev/null 2>&1 || :")
            rpm_kwargs["preun_scriptlet"] = "if test \"$1\" -eq 0; then systemctl disable --now {} >/dev/null 2>&1 || :; fi".format(quoted_service)
            rpm_kwargs["postun_scriptlet"] = "systemctl daemon-reload >/dev/null 2>&1 || :"
        if rpm_post_scriptlet:
            rpm_kwargs["post_scriptlet"] = "\n".join(rpm_post_scriptlet)
        pkg_rpm(**rpm_kwargs)

        transitioned_rpm = "{}_rpm_{}_transitioned".format(name, rpm_arch)
        package_artifact(
            name = transitioned_rpm,
            arch = arch,
            out = "{}_{}.rpm".format(package_name, rpm_arch),
            package = ":" + rpm_rule,
        )

        rpm_target = "{}_rpm_{}".format(name, rpm_arch)
        native.filegroup(
            name = rpm_target,
            srcs = [":" + transitioned_rpm],
            visibility = visibility,
        )
        rpm_targets.append(":" + rpm_target)
        all_targets.append(":" + rpm_target)

        rpm_test = "{}_rpm_{}_install_test".format(name, rpm_arch)
        _package_install_test(
            name = rpm_test,
            package = ":" + rpm_target,
            package_format = "rpm",
            package_name = package_name,
            docker_platform = docker_platform,
            image = _FEDORA_TEST_IMAGE,
            files = package_files,
            install_test_command = install_test_command,
            systemd_account = systemd_account,
            systemd_service = systemd_service,
            visibility = visibility,
        )
        install_tests.append(":" + rpm_test)

    native.filegroup(
        name = name + "_debs",
        srcs = deb_targets,
        visibility = visibility,
    )
    native.filegroup(
        name = name + "_rpms",
        srcs = rpm_targets,
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
        deb_depends = [],
        install_test_command = [],
        rpm_requires = [],
        systemd_account = None,
        systemd_service = None,
        target_name = None,
        visibility = None):
    """Packages an architecture-specific Linux binary under /usr/bin."""
    linux_packages(
        name = target_name or name,
        package_name = package_name,
        description = description,
        files = [
            package_file(
                src = binary,
                destination = "/usr/bin/" + executable_name,
                mode = "0755",
            ),
        ] + extra_files,
        deb_depends = deb_depends,
        install_test_command = install_test_command,
        rpm_requires = rpm_requires,
        systemd_account = systemd_account,
        systemd_service = systemd_service,
        visibility = visibility,
    )

def linux_runtime_packages(
        name,
        package_name,
        executable_name,
        command,
        description,
        files,
        deb_depends = [],
        install_test_command = [],
        rpm_requires = [],
        target_name = None,
        visibility = None):
    """Packages runtime files and a generated /usr/bin launcher."""
    package_target_name = target_name or name
    launcher_name = package_target_name + "_package_launcher"
    write_file(
        name = launcher_name,
        out = launcher_name + ".sh",
        content = [
            "#!/bin/sh",
            "exec {} \"$@\"".format(" ".join([_shell_quote(arg) for arg in command])),
        ],
        is_executable = True,
    )
    linux_packages(
        name = package_target_name,
        package_name = package_name,
        description = description,
        files = [
            package_file(
                src = ":" + launcher_name,
                destination = "/usr/bin/" + executable_name,
                mode = "0755",
            ),
        ] + files,
        deb_depends = deb_depends,
        install_test_command = install_test_command,
        rpm_requires = rpm_requires,
        visibility = visibility,
    )
