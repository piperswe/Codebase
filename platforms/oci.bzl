"""Helpers for building multi-architecture OCI images."""

load("@rules_oci//oci:defs.bzl", "oci_image", "oci_image_index")
load("@tar.bzl", "tar")

_LINUX_ARCHITECTURES = [
    ("amd64", "linux_amd64"),
    ("arm64", "linux_arm64_v8"),
]

def binary_oci_image(name, binary, base, entrypoint, visibility = None):
    """Packages a binary in linux/amd64 and linux/arm64 OCI images."""
    images = []
    destination = entrypoint[-1].lstrip("/")

    for arch, base_suffix in _LINUX_ARCHITECTURES:
        arch_name = "{}_linux_{}".format(name, arch)
        layer_name = "{}_layer".format(arch_name)
        binary_label = binary.format(arch = arch)

        tar(
            name = layer_name,
            mtree = [
                "./{} uid=0 gid=0 mode=0755 time=0 type=file content=$(location {})".format(destination, binary_label),
            ],
            srcs = [binary_label],
        )

        oci_image(
            name = arch_name,
            base = "@{}_{}".format(base, base_suffix),
            entrypoint = entrypoint,
            tars = [":" + layer_name],
        )
        images.append(":" + arch_name)

    oci_image_index(
        name = name,
        images = images,
        visibility = visibility,
    )

def multiarch_oci_image(name, base, tars, entrypoint, cmd = None, workdir = None, visibility = None):
    """Builds an OCI image index from architecture-specific layer targets."""
    images = []

    for arch, base_suffix in _LINUX_ARCHITECTURES:
        arch_name = "{}_linux_{}".format(name, arch)
        kwargs = {
            "name": arch_name,
            "base": "@{}_{}".format(base, base_suffix),
            "entrypoint": entrypoint,
            "tars": [tar.format(arch = arch) for tar in tars],
        }
        if cmd != None:
            kwargs["cmd"] = cmd
        if workdir != None:
            kwargs["workdir"] = workdir

        oci_image(**kwargs)
        images.append(":" + arch_name)

    oci_image_index(
        name = name,
        images = images,
        visibility = visibility,
    )
