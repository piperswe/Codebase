"""Repository defaults for TypeScript targets."""

load("@aspect_rules_ts//ts:defs.bzl", _ts_project = "ts_project")
load("@bazel_skylib//lib:partial.bzl", "partial")
load(":oxc.bzl", "oxc")

def ts_project(
        name,
        tsconfig = None,
        transpiler = None,
        source_map = False,
        **kwargs):
    """Creates a ts_project using the repository's Oxc transpiler by default."""
    if transpiler == None:
        if tsconfig == None:
            fail("tsconfig must be set when using the default Oxc transpiler")
        if kwargs.get("out_dir"):
            fail("out_dir is unsupported by the default in-place Oxc transpiler")
        if kwargs.get("root_dir"):
            fail("root_dir is unsupported by the default in-place Oxc transpiler")
        transpiler = partial.make(
            oxc,
            tsconfig = tsconfig,
            source_map = source_map,
        )

    _ts_project(
        name = name,
        tsconfig = tsconfig,
        transpiler = transpiler,
        source_map = source_map,
        **kwargs
    )
