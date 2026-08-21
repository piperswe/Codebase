use std::{env, path::PathBuf, process::ExitCode};

use oxc_transpiler::{transpile_file, TranspileRequest};

fn main() -> ExitCode {
    match parse_args().and_then(transpile_file) {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("oxc_transpiler: {error}");
            ExitCode::FAILURE
        }
    }
}

fn parse_args() -> Result<TranspileRequest, String> {
    let mut args = env::args().skip(1);
    let mut source = None;
    let mut source_path = None;
    let mut output = None;
    let mut source_map = None;
    let mut tsconfig = None;
    let mut tsconfig_path = None;
    let mut workspace = None;

    while let Some(flag) = args.next() {
        let value = args
            .next()
            .ok_or_else(|| format!("missing value for {flag}"))?;
        match flag.as_str() {
            "--source" => source = Some(PathBuf::from(value)),
            "--source-path" => source_path = Some(PathBuf::from(value)),
            "--output" => output = Some(PathBuf::from(value)),
            "--source-map" => source_map = Some(PathBuf::from(value)),
            "--tsconfig" => tsconfig = Some(PathBuf::from(value)),
            "--tsconfig-path" => tsconfig_path = Some(PathBuf::from(value)),
            "--workspace" => workspace = Some(PathBuf::from(value)),
            _ => return Err(format!("unknown argument {flag}")),
        }
    }

    Ok(TranspileRequest {
        source: source.ok_or("missing --source")?,
        source_path: source_path.ok_or("missing --source-path")?,
        output: output.ok_or("missing --output")?,
        source_map,
        tsconfig: tsconfig.ok_or("missing --tsconfig")?,
        tsconfig_path: tsconfig_path.ok_or("missing --tsconfig-path")?,
        workspace: workspace.ok_or("missing --workspace")?,
    })
}
