#!/bin/sh

set -eu

if [ "$#" -lt 10 ]; then
  echo "usage: $0 FORMAT PACKAGE_NAME SERVICE ACCOUNT_USER ACCOUNT_GROUP ACCOUNT_HOME ACCOUNT_SHELL STATE_DIRECTORY STATE_MODE EXPECTED_FILE_COUNT [EXPECTED_FILE ...] [SMOKE_ARG ...]" >&2
  exit 2
fi

package_format="$1"
package_name="$2"
systemd_service="$3"
systemd_account_user="$4"
systemd_account_group="$5"
systemd_account_home="$6"
systemd_account_shell="$7"
systemd_state_directory="$8"
systemd_state_mode="$9"
expected_file_count="${10}"
shift 10

case "${package_format}" in
  deb)
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y /tmp/package.deb
    dpkg-query --show --showformat='${Status}\n' "${package_name}" | grep -qx 'install ok installed'
    ;;
  rpm)
    dnf install -y /tmp/package.rpm
    rpm --query "${package_name}"
    ;;
  *)
    echo "unsupported package format: ${package_format}" >&2
    exit 2
    ;;
esac

while [ "${expected_file_count}" -gt 0 ]; do
  if [ "$#" -eq 0 ]; then
    echo "missing expected package file argument" >&2
    exit 2
  fi
  expected_file="$1"
  shift
  if [ ! -e "${expected_file}" ]; then
    echo "package did not install expected file: ${expected_file}" >&2
    exit 1
  fi
  expected_file_count=$((expected_file_count - 1))
done

if [ "${systemd_account_user}" != "-" ]; then
  passwd_entry="$(getent passwd "${systemd_account_user}")"
  getent group "${systemd_account_group}" >/dev/null
  account_uid="$(id -u "${systemd_account_user}")"
  account_primary_group="$(id -gn "${systemd_account_user}")"
  account_home="$(printf '%s\n' "${passwd_entry}" | cut -d: -f6)"
  account_shell="$(printf '%s\n' "${passwd_entry}" | cut -d: -f7)"

  if [ "${account_uid}" -eq 0 ] || [ "${account_uid}" -ge 1000 ]; then
    echo "package did not create a system UID for ${systemd_account_user}: ${account_uid}" >&2
    exit 1
  fi
  if [ "${account_primary_group}" != "${systemd_account_group}" ]; then
    echo "package gave ${systemd_account_user} the wrong primary group: ${account_primary_group}" >&2
    exit 1
  fi
  if [ "${account_home}" != "${systemd_account_home}" ]; then
    echo "package gave ${systemd_account_user} the wrong home: ${account_home}" >&2
    exit 1
  fi
  if [ "${account_shell}" != "${systemd_account_shell}" ]; then
    echo "package gave ${systemd_account_user} the wrong shell: ${account_shell}" >&2
    exit 1
  fi
fi

if [ "${systemd_state_directory}" != "-" ]; then
  state_metadata="$(stat --format='%U:%G:%a' "${systemd_state_directory}")"
  normalized_state_mode="${systemd_state_mode#0}"
  expected_state_metadata="${systemd_account_user}:${systemd_account_group}:${normalized_state_mode}"
  if [ "${state_metadata}" != "${expected_state_metadata}" ]; then
    echo "package created ${systemd_state_directory} with ${state_metadata}; expected ${expected_state_metadata}" >&2
    exit 1
  fi
fi

if [ "${systemd_service}" != "-" ]; then
  systemd-analyze verify "${systemd_service}"
  enabled_state="$(systemctl is-enabled "${systemd_service}" 2>/dev/null || true)"
  if [ "${enabled_state}" = "enabled" ]; then
    echo "package unexpectedly enabled ${systemd_service}" >&2
    exit 1
  fi
fi

if [ "$#" -gt 0 ]; then
  "$@"
fi
