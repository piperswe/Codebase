use std::{
    borrow::Cow,
    fs,
    path::{Component, Path, PathBuf},
};

use oxc::{
    allocator::Allocator,
    ast::ast::{
        ExportAllDeclaration, ExportFromDeclaration, Expression, ImportDeclaration,
        ImportExpression, Program, StringLiteral,
    },
    ast_visit::{walk_mut, VisitMut},
    codegen::{Codegen, CodegenOptions, CodegenReturn},
    parser::Parser,
    semantic::SemanticBuilder,
    span::SourceType,
    transformer::{
        ESTarget, JsxOptions, JsxRuntime, RewriteExtensionsMode, TransformOptions, Transformer,
    },
};
use oxc_resolver::{
    ResolveOptions, Resolver, TsConfig, TsconfigDiscovery, TsconfigOptions, TsconfigReferences,
};

pub struct TranspileRequest {
    pub source: PathBuf,
    pub source_path: PathBuf,
    pub output: PathBuf,
    pub source_map: Option<PathBuf>,
    pub tsconfig: PathBuf,
    pub tsconfig_path: PathBuf,
    pub workspace: PathBuf,
}

pub fn transpile_file(request: TranspileRequest) -> Result<(), String> {
    let workspace = absolute(&request.workspace)?;
    let source_file = canonical_from(&workspace, &request.source)?;
    let tsconfig_file = canonical_from(&workspace, &request.tsconfig)?;
    let source_text = fs::read_to_string(&source_file)
        .map_err(|error| format!("failed to read {}: {error}", source_file.display()))?;

    validate_logical_path(&request.source_path, "source")?;
    validate_logical_path(&request.tsconfig_path, "tsconfig")?;

    let config_tree_root = config_tree_root(&tsconfig_file, &request.tsconfig_path)?;
    let resolver = Resolver::new(ResolveOptions {
        tsconfig: Some(TsconfigDiscovery::Manual(TsconfigOptions {
            config_file: tsconfig_file.clone(),
            references: TsconfigReferences::Auto,
        })),
        extensions: vec![
            ".ts".into(),
            ".tsx".into(),
            ".mts".into(),
            ".cts".into(),
            ".js".into(),
            ".mjs".into(),
            ".cjs".into(),
        ],
        ..ResolveOptions::default()
    });
    let tsconfig = resolver.resolve_tsconfig(&tsconfig_file).map_err(|error| {
        format!(
            "failed to load {}: {error}",
            request.tsconfig_path.display()
        )
    })?;

    validate_config(&tsconfig)?;
    let transform_options = transform_options(&tsconfig)?;
    let output = transpile_source(
        &source_text,
        &request.source_path,
        &config_tree_root,
        &tsconfig,
        transform_options,
        request
            .source_map
            .as_ref()
            .map(|map_path| SourceMapRequest {
                output_name: request.output.file_name().and_then(|name| name.to_str()),
                map_name: map_path.file_name().and_then(|name| name.to_str()),
            }),
    )?;

    let mut code = output.code;
    if let Some(map_path) = &request.source_map {
        let map_name = map_path
            .file_name()
            .and_then(|name| name.to_str())
            .ok_or_else(|| format!("invalid source map output path {}", map_path.display()))?;
        if !code.ends_with('\n') {
            code.push('\n');
        }
        code.push_str("//# sourceMappingURL=");
        code.push_str(map_name);
        code.push('\n');

        let map = output
            .map
            .ok_or("Oxc did not produce the requested source map")?;
        fs::write(map_path, map)
            .map_err(|error| format!("failed to write {}: {error}", map_path.display()))?;
    }

    fs::write(&request.output, code)
        .map_err(|error| format!("failed to write {}: {error}", request.output.display()))
}

struct TranspileOutput {
    code: String,
    map: Option<String>,
}

struct SourceMapRequest<'a> {
    output_name: Option<&'a str>,
    map_name: Option<&'a str>,
}

fn transpile_source<'a>(
    source_text: &'a str,
    source_path: &Path,
    config_tree_root: &Path,
    tsconfig: &TsConfig,
    transform_options: TransformOptions,
    source_map: Option<SourceMapRequest<'_>>,
) -> Result<TranspileOutput, String> {
    let allocator = Allocator::default();
    let source_type = SourceType::from_path(source_path).map_err(|_| {
        format!(
            "unsupported TypeScript source path {}",
            source_path.display()
        )
    })?;
    let parsed = Parser::new(&allocator, source_text, source_type).parse();
    if !parsed.diagnostics.is_empty() {
        return Err(render_diagnostics(
            "parser",
            parsed.diagnostics,
            source_text,
        ));
    }
    let mut program = parsed.program;

    let mut rewriter = AliasRewriter {
        allocator: &allocator,
        config_tree_root,
        source_path,
        tsconfig,
        error: None,
    };
    rewriter.visit_program(&mut program);
    if let Some(error) = rewriter.error {
        return Err(error);
    }

    let semantic = SemanticBuilder::new()
        .with_excess_capacity(2.0)
        .with_enum_eval(true)
        .with_check_syntax_error(true)
        .build(&program);
    if !semantic.diagnostics.is_empty() {
        return Err(render_diagnostics(
            "semantic",
            semantic.diagnostics,
            source_text,
        ));
    }

    let transformed = Transformer::new(&allocator, source_path, &transform_options)
        .build_with_scoping(semantic.semantic.into_scoping(), &mut program);
    if !transformed.diagnostics.is_empty() {
        return Err(render_diagnostics(
            "transformer",
            transformed.diagnostics,
            source_text,
        ));
    }

    codegen(&program, source_text, source_path, source_map)
}

fn codegen<'a>(
    program: &Program<'a>,
    source_text: &'a str,
    source_path: &Path,
    source_map: Option<SourceMapRequest<'_>>,
) -> Result<TranspileOutput, String> {
    let options = CodegenOptions {
        source_map_path: source_map.as_ref().map(|_| source_path.to_path_buf()),
        ..CodegenOptions::default()
    };
    let CodegenReturn { code, mut map, .. } = Codegen::new().with_options(options).build(program);
    let map = match (map.as_mut(), source_map) {
        (Some(map), Some(request)) => {
            let output_name = request
                .output_name
                .ok_or("invalid JavaScript output filename")?;
            request
                .map_name
                .ok_or("invalid source map output filename")?;
            map.set_file(output_name);
            map.set_sources([path_string(source_path)?]);
            map.set_source_contents(vec![Some(source_text)]);
            Some(map.to_json_string())
        }
        (None, Some(_)) => return Err("Oxc did not produce the requested source map".to_owned()),
        _ => None,
    };
    Ok(TranspileOutput { code, map })
}

fn render_diagnostics(
    stage: &str,
    diagnostics: impl IntoIterator<Item = oxc::diagnostics::OxcDiagnostic>,
    source_text: &str,
) -> String {
    let rendered = diagnostics
        .into_iter()
        .map(|diagnostic| diagnostic.render_with_source_code(source_text.to_owned()))
        .collect::<Vec<_>>()
        .join("\n");
    format!("{stage} diagnostics:\n{rendered}")
}

fn validate_config(tsconfig: &TsConfig) -> Result<(), String> {
    let options = &tsconfig.compiler_options;
    if let Some(out_dir) = &options.out_dir {
        return Err(format!(
            "compilerOptions.outDir is unsupported by the in-place Oxc transpiler: {}",
            out_dir.display()
        ));
    }
    if let Some(module) = &options.module {
        let module = module.to_ascii_lowercase();
        if !matches!(
            module.as_str(),
            "es6" | "es2015" | "es2020" | "es2022" | "esnext" | "preserve"
        ) {
            return Err(format!(
                "compilerOptions.module={module:?} is unsupported; the Oxc transpiler preserves ESM"
            ));
        }
    }
    if let Some(paths) = &options.paths {
        for (alias, targets) in paths {
            if alias.matches('*').count() > 1 {
                return Err(format!(
                    "compilerOptions.paths alias {alias:?} has more than one wildcard"
                ));
            }
            if targets.len() != 1 {
                return Err(format!(
                    "compilerOptions.paths alias {alias:?} must have exactly one target, found {}",
                    targets.len()
                ));
            }
            if targets[0].to_string_lossy().matches('*').count() > 1 {
                return Err(format!(
                    "compilerOptions.paths target for {alias:?} has more than one wildcard"
                ));
            }
        }
    }
    Ok(())
}

fn transform_options(tsconfig: &TsConfig) -> Result<TransformOptions, String> {
    let compiler = &tsconfig.compiler_options;
    let target = compiler
        .target
        .as_deref()
        .unwrap_or("es5")
        .to_ascii_lowercase();
    let es_target = target
        .parse::<ESTarget>()
        .map_err(|error| format!("unsupported compilerOptions.target={target:?}: {error}"))?;
    let mut options = TransformOptions::from(es_target);

    options.cwd = tsconfig.directory().to_path_buf();
    options.decorator.legacy = compiler.experimental_decorators.unwrap_or(false);
    options.decorator.emit_decorator_metadata = compiler.emit_decorator_metadata.unwrap_or(false);
    options.decorator.strict_null_checks = compiler
        .strict_null_checks
        .or(compiler.strict)
        .unwrap_or(true);

    let use_define_for_class_fields = compiler
        .use_define_for_class_fields
        .unwrap_or_else(|| target_at_least_es2022(&target));
    options.assumptions.set_public_class_fields = !use_define_for_class_fields;
    options.typescript.remove_class_fields_without_initializer = !use_define_for_class_fields;
    options.typescript.only_remove_type_imports = compiler.verbatim_module_syntax.unwrap_or(false)
        || compiler.preserve_value_imports.unwrap_or(false)
        || compiler.imports_not_used_as_values.as_deref() == Some("preserve");
    options.typescript.rewrite_import_extensions = compiler
        .rewrite_relative_import_extensions
        .unwrap_or(false)
        .then_some(RewriteExtensionsMode::Rewrite);

    if let Some(factory) = &compiler.jsx_factory {
        options.typescript.jsx_pragma = Cow::Owned(factory.clone());
    }
    if let Some(fragment) = &compiler.jsx_fragment_factory {
        options.typescript.jsx_pragma_frag = Cow::Owned(fragment.clone());
    }
    configure_jsx(
        &mut options,
        compiler.jsx.as_deref(),
        compiler.jsx_import_source.as_deref(),
    )?;
    Ok(options)
}

fn configure_jsx(
    options: &mut TransformOptions,
    jsx: Option<&str>,
    import_source: Option<&str>,
) -> Result<(), String> {
    match jsx.map(str::to_ascii_lowercase).as_deref() {
        None | Some("preserve" | "react-native") => options.jsx = JsxOptions::disable(),
        Some("react") => {
            options.jsx = JsxOptions::enable();
            options.jsx.runtime = JsxRuntime::Classic;
        }
        Some("react-jsx" | "react-jsxdev") => {
            options.jsx = JsxOptions::enable();
            options.jsx.runtime = JsxRuntime::Automatic;
            options.jsx.development =
                jsx.is_some_and(|value| value.eq_ignore_ascii_case("react-jsxdev"));
            options.jsx.import_source = import_source.map(str::to_owned);
        }
        Some(value) => return Err(format!("unsupported compilerOptions.jsx={value:?}")),
    }
    if options.jsx.runtime == JsxRuntime::Classic {
        options.jsx.pragma = Some(options.typescript.jsx_pragma.to_string());
        options.jsx.pragma_frag = Some(options.typescript.jsx_pragma_frag.to_string());
    }
    Ok(())
}

fn target_at_least_es2022(target: &str) -> bool {
    target == "esnext"
        || target
            .strip_prefix("es")
            .and_then(|year| year.parse::<u32>().ok())
            .is_some_and(|year| year >= 2022)
}

struct AliasRewriter<'a, 'config> {
    allocator: &'a Allocator,
    config_tree_root: &'config Path,
    source_path: &'config Path,
    tsconfig: &'config TsConfig,
    error: Option<String>,
}

impl<'a> AliasRewriter<'a, '_> {
    fn rewrite_literal(&mut self, literal: &mut StringLiteral<'a>) {
        if self.error.is_some() {
            return;
        }
        match self.rewrite_specifier(literal.value.as_ref()) {
            Ok(Some(specifier)) => {
                literal.value = self.allocator.alloc_str(&specifier).into();
                literal.raw = None;
            }
            Ok(None) => {}
            Err(error) => self.error = Some(error),
        }
    }

    fn rewrite_specifier(&self, specifier: &str) -> Result<Option<String>, String> {
        let Some(target) = matching_alias_target(self.tsconfig, specifier)? else {
            return Ok(None);
        };
        let target = normalize_path(&target)?;
        let logical_target = target.strip_prefix(self.config_tree_root).map_err(|_| {
            format!(
                "path alias {specifier:?} resolves outside the Bazel workspace: {}",
                target.display()
            )
        })?;
        let source_dir = self.source_path.parent().unwrap_or(Path::new(""));
        Ok(Some(relative_specifier(source_dir, logical_target)?))
    }
}

impl<'a> VisitMut<'a> for AliasRewriter<'a, '_> {
    fn visit_import_declaration(&mut self, declaration: &mut ImportDeclaration<'a>) {
        self.rewrite_literal(&mut declaration.source);
        walk_mut::walk_import_declaration(self, declaration);
    }

    fn visit_export_from_declaration(&mut self, declaration: &mut ExportFromDeclaration<'a>) {
        self.rewrite_literal(&mut declaration.source);
        walk_mut::walk_export_from_declaration(self, declaration);
    }

    fn visit_export_all_declaration(&mut self, declaration: &mut ExportAllDeclaration<'a>) {
        self.rewrite_literal(&mut declaration.source);
        walk_mut::walk_export_all_declaration(self, declaration);
    }

    fn visit_import_expression(&mut self, expression: &mut ImportExpression<'a>) {
        if let Expression::StringLiteral(literal) = &mut expression.source {
            self.rewrite_literal(literal);
        }
        walk_mut::walk_import_expression(self, expression);
    }
}

fn matching_alias_target(tsconfig: &TsConfig, specifier: &str) -> Result<Option<PathBuf>, String> {
    let Some(paths) = &tsconfig.compiler_options.paths else {
        return Ok(None);
    };
    let mut best: Option<(&str, &PathBuf, &str)> = None;
    for (pattern, targets) in paths {
        let target = &targets[0];
        let wildcard = match_alias(pattern, specifier);
        if let Some(wildcard) = wildcard {
            let score = pattern.len() - usize::from(pattern.contains('*'));
            if best.is_none_or(|(best_pattern, _, _)| {
                score > best_pattern.len() - usize::from(best_pattern.contains('*'))
            }) {
                best = Some((pattern, target, wildcard));
            }
        }
    }
    let Some((_pattern, target, wildcard)) = best else {
        return Ok(None);
    };
    let target = target.to_string_lossy();
    Ok(Some(PathBuf::from(target.replacen('*', wildcard, 1))))
}

fn match_alias<'a>(pattern: &str, specifier: &'a str) -> Option<&'a str> {
    let Some((prefix, suffix)) = pattern.split_once('*') else {
        return (pattern == specifier).then_some("");
    };
    specifier
        .strip_prefix(prefix)
        .and_then(|rest| rest.strip_suffix(suffix))
}

fn relative_specifier(from: &Path, to: &Path) -> Result<String, String> {
    let from = normal_components(from)?;
    let to = normal_components(to)?;
    let common = from
        .iter()
        .zip(&to)
        .take_while(|(left, right)| left == right)
        .count();
    let mut parts = vec!["..".to_owned(); from.len() - common];
    parts.extend(to[common..].iter().cloned());
    let path = parts.join("/");
    if path.starts_with('.') {
        Ok(path)
    } else {
        Ok(format!("./{path}"))
    }
}

fn config_tree_root(actual: &Path, logical: &Path) -> Result<PathBuf, String> {
    if !actual.ends_with(logical) {
        return Err(format!(
            "tsconfig input {} does not end with its logical workspace path {}",
            actual.display(),
            logical.display()
        ));
    }
    let mut root = actual.to_path_buf();
    for _ in logical.components() {
        root.pop();
    }
    normalize_path(&root)
}

fn validate_logical_path(path: &Path, kind: &str) -> Result<(), String> {
    if path.is_absolute() || normal_components(path).is_err() {
        return Err(format!(
            "{kind} logical path must stay within the Bazel workspace: {}",
            path.display()
        ));
    }
    Ok(())
}

fn normal_components(path: &Path) -> Result<Vec<String>, String> {
    let mut components = Vec::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::Normal(value) => components.push(value.to_string_lossy().into_owned()),
            Component::ParentDir => {
                if components.pop().is_none() {
                    return Err(format!("path escapes its root: {}", path.display()));
                }
            }
            Component::RootDir | Component::Prefix(_) => {
                return Err(format!(
                    "expected a relative path, found {}",
                    path.display()
                ));
            }
        }
    }
    Ok(components)
}

fn normalize_path(path: &Path) -> Result<PathBuf, String> {
    if !path.is_absolute() {
        return Err(format!(
            "expected an absolute path, found {}",
            path.display()
        ));
    }
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::Prefix(prefix) => normalized.push(prefix.as_os_str()),
            Component::RootDir => normalized.push(Path::new("/")),
            Component::CurDir => {}
            Component::ParentDir => {
                if !normalized.pop() {
                    return Err(format!("path escapes its root: {}", path.display()));
                }
            }
            Component::Normal(value) => normalized.push(value),
        }
    }
    Ok(normalized)
}

fn absolute(path: &Path) -> Result<PathBuf, String> {
    let normalized = if path.is_absolute() {
        normalize_path(path)
    } else {
        let cwd =
            std::env::current_dir().map_err(|error| format!("failed to read cwd: {error}"))?;
        normalize_path(&cwd.join(path))
    }?;
    fs::canonicalize(&normalized).map_err(|error| {
        format!(
            "failed to resolve absolute path {}: {error}",
            normalized.display()
        )
    })
}

fn canonical_from(base: &Path, path: &Path) -> Result<PathBuf, String> {
    let path = if path.is_absolute() {
        path.to_path_buf()
    } else {
        base.join(path)
    };
    fs::canonicalize(&path)
        .map_err(|error| format!("failed to resolve {}: {error}", path.display()))
}

fn path_string(path: &Path) -> Result<String, String> {
    path.to_str()
        .map(str::to_owned)
        .ok_or_else(|| format!("path is not valid UTF-8: {}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        sync::atomic::{AtomicU64, Ordering},
        time::{SystemTime, UNIX_EPOCH},
    };

    static NEXT_TEMP: AtomicU64 = AtomicU64::new(0);

    struct Fixture {
        root: PathBuf,
    }

    impl Fixture {
        fn new() -> Self {
            let unique = NEXT_TEMP.fetch_add(1, Ordering::Relaxed);
            let nanos = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos();
            let root = std::env::temp_dir().join(format!(
                "oxc_transpiler_{}_{}_{}",
                std::process::id(),
                nanos,
                unique
            ));
            fs::create_dir_all(&root).unwrap();
            Self { root }
        }

        fn write(&self, path: &str, content: &str) {
            let path = self.root.join(path);
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(path, content).unwrap();
        }

        fn transpile(
            &self,
            source: &str,
            config: &str,
            source_map: bool,
        ) -> Result<String, String> {
            self.write("tsconfig.json", config);
            self.write("pkg/input.ts", source);
            let output = self.root.join("pkg/input.js");
            let map = source_map.then(|| self.root.join("pkg/input.js.map"));
            transpile_file(TranspileRequest {
                source: self.root.join("pkg/input.ts"),
                source_path: PathBuf::from("pkg/input.ts"),
                output: output.clone(),
                source_map: map,
                tsconfig: self.root.join("tsconfig.json"),
                tsconfig_path: PathBuf::from("tsconfig.json"),
                workspace: self.root.clone(),
            })?;
            fs::read_to_string(output).map_err(|error| error.to_string())
        }
    }

    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    const CONFIG: &str = r#"{
        "compilerOptions": {
            "target": "esnext",
            "module": "esnext",
            "paths": {"$/*": ["./*"]}
        }
    }"#;

    #[test]
    fn erases_types() {
        let fixture = Fixture::new();
        let output = fixture
            .transpile("const answer: number = 42;", CONFIG, false)
            .unwrap();
        assert!(output.contains("const answer = 42;"), "{output}");
        assert!(!output.contains(": number"), "{output}");
    }

    #[test]
    fn inherits_effective_tsconfig() {
        let fixture = Fixture::new();
        fixture.write(
            "base.json",
            r#"{"compilerOptions":{"target":"es2015","module":"esnext","paths":{"$/*":["./*"]}}}"#,
        );
        let output = fixture
            .transpile(
                "const value = object?.value;",
                r#"{"extends":"./base.json"}"#,
                false,
            )
            .unwrap();
        assert!(output.contains("_object = object"), "{output}");
    }

    #[test]
    fn rewrites_exact_and_wildcard_aliases_in_all_runtime_imports() {
        let fixture = Fixture::new();
        let config = r#"{
            "compilerOptions": {
                "target": "esnext",
                "module": "esnext",
                "paths": {"exact": ["./lib/exact.js"], "$/*": ["./*"]}
            }
        }"#;
        let source = r#"
            import value from "exact";
            export { other } from "$/lib/other.js";
            export * from "$/lib/all.js";
            const lazy = import("$/lib/lazy.js");
            console.log(value, lazy);
        "#;
        let output = fixture.transpile(source, config, false).unwrap();
        assert!(output.contains("../lib/exact.js"), "{output}");
        assert!(output.contains("../lib/other.js"), "{output}");
        assert!(output.contains("../lib/all.js"), "{output}");
        assert!(output.contains("../lib/lazy.js"), "{output}");
    }

    #[test]
    fn fails_on_parser_semantic_and_transformer_diagnostics() {
        let fixture = Fixture::new();
        let parser = fixture.transpile("const = ;", CONFIG, false).unwrap_err();
        assert!(parser.contains("parser diagnostics"), "{parser}");

        let semantic = fixture
            .transpile("break missingLabel;", CONFIG, false)
            .unwrap_err();
        assert!(semantic.contains("semantic diagnostics"), "{semantic}");

        let transformer = fixture
            .transpile("namespace Example { export let value = 1; }", CONFIG, false)
            .unwrap_err();
        assert!(
            transformer.contains("transformer diagnostics"),
            "{transformer}"
        );
    }

    #[test]
    fn emits_external_source_map_with_original_source() {
        let fixture = Fixture::new();
        let source = "import value from \"$/lib/value.js\";\nconst typed: number = value;";
        let output = fixture.transpile(source, CONFIG, true).unwrap();
        assert!(output.contains("sourceMappingURL=input.js.map"), "{output}");
        let map = fs::read_to_string(fixture.root.join("pkg/input.js.map")).unwrap();
        assert!(map.contains("pkg/input.ts"), "{map}");
        assert!(map.contains("$/lib/value.js"), "{map}");
    }

    #[test]
    fn rejects_alias_fallbacks_and_outside_targets() {
        let fixture = Fixture::new();
        let fallback = fixture
            .transpile(
                "import value from \"alias\";",
                r#"{"compilerOptions":{"target":"esnext","module":"esnext","paths":{"alias":["./a.js","./b.js"]}}}"#,
                false,
            )
            .unwrap_err();
        assert!(fallback.contains("exactly one target"), "{fallback}");

        let outside = fixture
            .transpile(
                "import value from \"alias\";",
                r#"{"compilerOptions":{"target":"esnext","module":"esnext","paths":{"alias":["../outside.js"]}}}"#,
                false,
            )
            .unwrap_err();
        assert!(outside.contains("outside the Bazel workspace"), "{outside}");
    }
}
