"""Oxc transpiler compatible with ts_project's custom transpiler contract."""

load("@aspect_rules_ts//ts:defs.bzl", "TsConfigInfo")
load("@aspect_rules_ts//ts/private:ts_lib.bzl", "lib")

def _is_runtime_typescript_source(file):
    return (
        (file.basename.endswith(".ts") or
         file.basename.endswith(".tsx") or
         file.basename.endswith(".mts") or
         file.basename.endswith(".cts")) and
        not file.basename.endswith(".d.ts") and
        not file.basename.endswith(".d.mts") and
        not file.basename.endswith(".d.cts")
    )

def _oxc_transpile_impl(ctx):
    srcs = [src for src in ctx.files.srcs if _is_runtime_typescript_source(src)]
    if len(srcs) != len(ctx.outputs.js_outs):
        fail("Oxc expected one JavaScript output for each TypeScript source")
    if ctx.attr.source_map and len(srcs) != len(ctx.outputs.map_outs):
        fail("Oxc expected one source map output for each TypeScript source")
    if not ctx.attr.source_map and ctx.outputs.map_outs:
        fail("Oxc received source map outputs while source_map is false")

    config_inputs = depset([ctx.file.tsconfig])
    if TsConfigInfo in ctx.attr.tsconfig:
        config_inputs = ctx.attr.tsconfig[TsConfigInfo].deps

    for index, src in enumerate(srcs):
        js_out = ctx.outputs.js_outs[index]
        map_out = ctx.outputs.map_outs[index] if ctx.attr.source_map else None
        args = ctx.actions.args()
        args.add("--source", src)
        args.add("--source-path", src.short_path)
        args.add("--output", js_out)
        if map_out:
            args.add("--source-map", map_out)
        args.add("--tsconfig", ctx.file.tsconfig)
        args.add("--tsconfig-path", ctx.file.tsconfig.short_path)
        args.add("--workspace", ".")

        outputs = [js_out]
        if map_out:
            outputs.append(map_out)
        ctx.actions.run(
            executable = ctx.executable._oxc,
            arguments = [args],
            inputs = depset([src], transitive = [config_inputs]),
            outputs = outputs,
            mnemonic = "OxcTranspile",
            progress_message = "Oxc transpiling %{input}",
        )

    return DefaultInfo(files = depset(ctx.outputs.js_outs + ctx.outputs.map_outs))

_oxc_transpile = rule(
    implementation = _oxc_transpile_impl,
    attrs = {
        "srcs": attr.label_list(
            allow_files = [".ts", ".tsx", ".mts", ".cts"],
            mandatory = True,
        ),
        "tsconfig": attr.label(
            allow_single_file = [".json"],
            mandatory = True,
        ),
        "source_map": attr.bool(),
        "js_outs": attr.output_list(mandatory = True),
        "map_outs": attr.output_list(),
        "_oxc": attr.label(
            cfg = "exec",
            executable = True,
            default = "//tools/typescript:oxc_transpiler",
        ),
    },
)

def oxc(name, srcs, tsconfig, source_map = False, **kwargs):
    """Transpiles TypeScript sources to in-place JavaScript with Oxc."""
    outs = lib.calculate_outs(
        srcs = srcs,
        out_dir = None,
        typings_out_dir = None,
        root_dir = None,
        allow_js = False,
        resolve_json_module = False,
        preserve_jsx = False,
        emit_declaration_only = False,
        source_map = source_map,
        declaration = False,
        composite = False,
        declaration_map = False,
        emit_js = True,
        emit_dts = False,
    )
    _oxc_transpile(
        name = name,
        srcs = srcs,
        tsconfig = tsconfig,
        source_map = source_map,
        js_outs = outs.js_outs,
        map_outs = outs.map_outs,
        **kwargs
    )
