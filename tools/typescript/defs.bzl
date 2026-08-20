"""Repository defaults for TypeScript targets."""

load("@aspect_rules_swc//swc:defs.bzl", "swc")
load("@aspect_rules_ts//ts:defs.bzl", _ts_project = "ts_project")
load("@bazel_skylib//lib:partial.bzl", "partial")
load("@npm//:tsconfig-to-swcconfig/package_json.bzl", _tsconfig_to_swcconfig = "bin")

def ts_project(name, tsconfig = None, transpiler = None, **kwargs):
    """Creates a ts_project that derives an SWC config from its tsconfig."""
    if transpiler == None:
        if tsconfig == None:
            fail("tsconfig must be set when using the default SWC transpiler")

        swcrc = "{}.swcrc".format(name)
        _tsconfig_to_swcconfig.t2s(
            name = "{}_swcrc".format(name),
            srcs = [tsconfig],
            args = [
                "--filename",
                "$(location {})".format(tsconfig),
            ],
            stdout = swcrc,
        )
        transpiler = partial.make(
            swc,
            swcrc = ":{}".format(swcrc),
        )

    _ts_project(
        name = name,
        tsconfig = tsconfig,
        transpiler = transpiler,
        **kwargs
    )
