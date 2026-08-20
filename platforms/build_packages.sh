#!/bin/sh

set -eu

if test "$(uname -s)" = "Darwin" && command -v brew >/dev/null 2>&1; then
  coreutils_prefix="$(brew --prefix coreutils)"
  PATH="${coreutils_prefix}/libexec/gnubin:${PATH}"
  export PATH
fi

if ! command -v rpmbuild >/dev/null 2>&1; then
  echo "rpmbuild is required to build RPM packages" >&2
  exit 1
fi

if [ "$#" -eq 0 ]; then
  set -- //:packages
fi

exec bazel build \
  --repo_env=PATH="$PATH" \
  --action_env=PATH="$PATH" \
  "$@"
