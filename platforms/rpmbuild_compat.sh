#!/bin/sh

set -eu

PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export PATH

rpmbuild_command=$(command -v rpmbuild)
rpm_command=$(command -v rpm)
host_arch=$($rpm_command --eval '%{_arch}')
target_arch=
install_script=
spec_file=
previous_argument=

for argument in "$@"; do
  if [ "$previous_argument" = "--define" ]; then
    case "$argument" in
      "_target_cpu "*) target_arch=${argument#_target_cpu } ;;
      "build_rpm_install "*) install_script=${argument#build_rpm_install } ;;
    esac
  fi
  case "$argument" in
    *.spec) spec_file=$argument ;;
  esac
  previous_argument=$argument
done

case $($rpmbuild_command --version) in
  "RPM version 6."*)
    if [ -n "$install_script" ] && [ -f "$install_script" ]; then
      sed "s|^cp '|cp '../|" "$install_script" >"$install_script.rpm6"
      mv "$install_script.rpm6" "$install_script"
    fi
    if [ -n "$spec_file" ] && [ -f "$spec_file" ]; then
      sed "s|%files -f %build_rpm_files|%files -f ../%build_rpm_files|" "$spec_file" >"$spec_file.rpm6"
      mv "$spec_file.rpm6" "$spec_file"
    fi
    ;;
esac

if [ -n "$target_arch" ] && [ "$target_arch" != "$host_arch" ]; then
  rpm_config_dir=$($rpm_command --eval '%{_rpmconfigdir}')
  rpmrc=$(mktemp "${TMPDIR:-/tmp}/rpmbuild-compat.XXXXXX")
  trap 'rm -f "$rpmrc"' EXIT HUP INT TERM

  awk -v host="$host_arch" -v target="$target_arch" '
    $1 == "buildarch_compat:" && $2 == host ":" {
      print $0 " " target
      found = 1
      next
    }
    { print }
    END {
      if (!found) {
        print "buildarch_compat: " host ": " target
      }
    }
  ' "$rpm_config_dir/rpmrc" >"$rpmrc"

  set -- "--rcfile=$rpmrc" --target "${target_arch}-linux" "$@"
  "$rpmbuild_command" "$@"
  exit
fi

exec "$rpmbuild_command" "$@"
