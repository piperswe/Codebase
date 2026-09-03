"""Helpers for building multi-architecture OCI images."""

load("@rules_oci//oci:defs.bzl", "oci_image", "oci_image_index")
load("@tar.bzl", "tar")

_LINUX_ARCHITECTURES = [
    ("amd64", "linux_amd64"),
    ("arm64", "linux_arm64_v8"),
]

_SOURCE_URL = "https://github.com/piperswe/Codebase"

_VENDOR = "piperswe"

# Annotation keys whose values come from the workspace status file.
_STATUS_ANNOTATIONS = {
    "org.opencontainers.image.revision": "STABLE_GIT_REVISION",
    "org.opencontainers.image.version": "STABLE_IMAGE_VERSION",
}

# Reads a workspace status value with the same awk expression that
# //platforms:package_version.bzl uses, so a missing key fails the build.
_ANNOTATIONS_COMMAND = """
set -eu
static="$1"
info="$2"
out="$3"
shift 3
cat "$static" > "$out"
for pair in "$@"; do
  key="${pair%%=*}"
  status_key="${pair#*=}"
  value="$(awk -v key="$status_key" '$1 == key {$1 = ""; sub(/^ /, ""); print; found = 1; exit} END {if (!found) exit 1}' "$info")"
  printf '%s=%s\\n' "$key" "$value" >> "$out"
done
"""

def _oci_annotations_impl(ctx):
    static = ctx.actions.declare_file(ctx.label.name + ".static.txt")
    ctx.actions.write(
        output = static,
        content = "".join([
            "{}={}\n".format(key, value)
            for key, value in sorted(ctx.attr.annotations.items())
        ]),
    )

    output = ctx.actions.declare_file(ctx.label.name + ".txt")
    ctx.actions.run_shell(
        inputs = [static, ctx.info_file],
        outputs = [output],
        arguments = [static.path, ctx.info_file.path, output.path] + [
            "{}={}".format(key, status_key)
            for key, status_key in sorted(ctx.attr.status_annotations.items())
        ],
        command = _ANNOTATIONS_COMMAND,
        mnemonic = "OCIAnnotations",
    )
    return [DefaultInfo(files = depset([output]))]

_oci_annotations = rule(
    implementation = _oci_annotations_impl,
    doc = "Writes an OCI `name=value` metadata file from static and stamped values.",
    attrs = {
        "annotations": attr.string_dict(
            doc = "Literal annotation keys and values.",
        ),
        "status_annotations": attr.string_dict(
            doc = "Maps an annotation key to a workspace status key.",
        ),
    },
)

def _image_annotations(name, title, description):
    """Declares the shared annotation file for one image and returns its label."""
    label = "{}_annotations".format(name)

    _oci_annotations(
        name = label,
        annotations = {
            "org.opencontainers.image.title": title,
            "org.opencontainers.image.description": description,
            "org.opencontainers.image.source": _SOURCE_URL,
            "org.opencontainers.image.vendor": _VENDOR,
        },
        status_annotations = _STATUS_ANNOTATIONS,
    )

    return ":" + label

def binary_oci_image(name, binary, base, entrypoint, title, description, visibility = None):
    """Packages a binary in linux/amd64 and linux/arm64 OCI images."""
    images = []
    destination = entrypoint[-1].lstrip("/")
    annotations = _image_annotations(name, title, description)

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
            annotations = annotations,
            base = "@{}_{}".format(base, base_suffix),
            entrypoint = entrypoint,
            labels = annotations,
            tars = [":" + layer_name],
        )
        images.append(":" + arch_name)

    oci_image_index(
        name = name,
        images = images,
        visibility = visibility,
    )

def multiarch_oci_image(name, base, tars, entrypoint, title, description, cmd = None, workdir = None, visibility = None):
    """Builds an OCI image index from architecture-specific layer targets."""
    images = []
    annotations = _image_annotations(name, title, description)

    for arch, base_suffix in _LINUX_ARCHITECTURES:
        arch_name = "{}_linux_{}".format(name, arch)
        kwargs = {
            "name": arch_name,
            "annotations": annotations,
            "base": "@{}_{}".format(base, base_suffix),
            "entrypoint": entrypoint,
            "labels": annotations,
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
