"""Wrangler rules used by generated BUILD files."""

load("@bazel_lib//lib:directory_path.bzl", _directory_path = "directory_path")
load("@bazel_lib//lib:write_source_files.bzl", _write_source_files = "write_source_files")
load("@npm//:wrangler/package_json.bzl", _wrangler = "bin")

directory_path = _directory_path
wrangler = _wrangler
write_source_files = _write_source_files
