#!/bin/sh

set -eu

if ! git_root="$(git rev-parse --show-toplevel 2>/dev/null)"; then
  echo "STABLE_DATASITE_VERSION 0.0.0"
  echo "STABLE_YAGO_VERSION 0.0.0"
  echo "STABLE_INTERFACE_VERSION 0.0.0"
  echo "STABLE_PACKAGE_VERSION 0.0.0"
  echo "STABLE_IMAGE_VERSION 0.0.0"
  echo "STABLE_GIT_REVISION unknown"
  exit 0
fi

cd "$git_root"

sha="$(git rev-parse --short=12 HEAD)"
revision="$(git rev-parse HEAD)"
tag="$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --abbrev=0 2>/dev/null || true)"

case "$tag" in
  v[0-9]*.[0-9]*.[0-9]*)
    base="${tag#v}"
    distance="$(git rev-list --count "$tag"..HEAD)"
    ;;
  *)
    base="0.0.0"
    distance="$(git rev-list --count HEAD)"
    ;;
esac

dirty=""
if test -n "$(git status --porcelain --untracked-files=normal)"; then
  dirty=".dirty"
fi

if test "$distance" -eq 0 && test -z "$dirty"; then
  package_version="$base"
else
  package_version="${base}+git.${distance}.g${sha}${dirty}"
fi

echo "STABLE_DATASITE_VERSION $package_version"
echo "STABLE_YAGO_VERSION $package_version"
echo "STABLE_INTERFACE_VERSION $package_version"
echo "STABLE_PACKAGE_VERSION $package_version"
echo "STABLE_IMAGE_VERSION $package_version"
echo "STABLE_GIT_REVISION $revision"
