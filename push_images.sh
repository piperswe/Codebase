#!/usr/bin/env bash

# --- begin runfiles.bash initialization v3 ---
set -uo pipefail
set +e
f=bazel_tools/tools/bash/runfiles/runfiles.bash
# shellcheck disable=SC1090
source "${RUNFILES_DIR:-/dev/null}/$f" 2>/dev/null || \
  source "$(grep -sm1 "^$f " "${RUNFILES_MANIFEST_FILE:-/dev/null}" | cut -f2- -d' ')" 2>/dev/null || \
  source "$0.runfiles/$f" 2>/dev/null || \
  source "$(grep -sm1 "^$f " "$0.runfiles_manifest" | cut -f2- -d' ')" 2>/dev/null || \
  source "$(grep -sm1 "^$f " "$0.exe.runfiles_manifest" | cut -f2- -d' ')" 2>/dev/null || \
  { echo >&2 "ERROR: cannot find $f"; exit 1; }
f=
set -e
# --- end runfiles.bash initialization v3 ---

runfiles_export_envvars

set -euo pipefail

images=()
while (( $# > 0 )) && [[ "$1" != -* ]]; do
  images+=("$1")
  shift
done

for image in "${images[@]}"; do
  pusher="${image%%=*}"
  if [[ ! -x "$pusher" ]]; then
    echo "Image pusher is not executable: $pusher" >&2
    exit 1
  fi
done

if [[ -z "${REGISTRY:-}" ]]; then
  echo "REGISTRY must name the destination container registry namespace" >&2
  exit 1
fi

registry="${REGISTRY%/}"
for image in "${images[@]}"; do
  pusher="${image%%=*}"
  repository="${image#*=}"
  "$pusher" --repository "$registry/$repository" "$@"
done
