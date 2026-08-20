#!/bin/sh

set -eu

if ! git_root="$(git rev-parse --show-toplevel 2>/dev/null)"; then
  echo "STABLE_DATASITE_VERSION 0.0.0"
  echo "STABLE_DEB_VERSION 0.0.0"
  echo "STABLE_RPM_VERSION 0.0.0"
  echo "STABLE_RPM_RELEASE 1"
  exit 0
fi

cd "$git_root"

sha="$(git rev-parse --short=12 HEAD)"
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
  deb_version="$base"
  rpm_release="1"
else
  deb_version="${base}+git.${distance}.g${sha}${dirty}"
  rpm_release="1.git.${distance}.g${sha}${dirty}"
fi

echo "STABLE_DATASITE_VERSION $deb_version"
echo "STABLE_DEB_VERSION $deb_version"
echo "STABLE_RPM_VERSION $base"
echo "STABLE_RPM_RELEASE $rpm_release"
