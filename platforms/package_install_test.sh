#!/usr/bin/env bash

set -euo pipefail

resolve_runfile() {
  local logical_path="$1"
  local candidate

  if [[ -n "${RUNFILES_DIR:-}" ]]; then
    candidate="${RUNFILES_DIR}/${logical_path}"
    if [[ -e "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return
    fi
  fi

  if [[ -n "${RUNFILES_MANIFEST_FILE:-}" ]]; then
    candidate="$(awk -v path="${logical_path}" '$1 == path { sub($1 " ", ""); print; exit }' "${RUNFILES_MANIFEST_FILE}")"
    if [[ -n "${candidate}" && -e "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return
    fi
  fi

  candidate="${0}.runfiles/${logical_path}"
  if [[ -e "${candidate}" ]]; then
    printf '%s\n' "${candidate}"
    return
  fi

  printf 'Unable to resolve runfile: %s\n' "${logical_path}" >&2
  return 1
}

if [[ "$#" -lt 14 ]]; then
  echo "usage: $0 FORMAT PLATFORM IMAGE PACKAGE CONTAINER_SCRIPT PACKAGE_NAME SERVICE ACCOUNT_USER ACCOUNT_GROUP ACCOUNT_HOME ACCOUNT_SHELL STATE_DIRECTORY STATE_MODE EXPECTED_FILE_COUNT [EXPECTED_FILE ...] [SMOKE_ARG ...]" >&2
  exit 2
fi

package_format="$1"
docker_platform="$2"
image="$3"
package_path="$(resolve_runfile "$4")"
container_script="$(resolve_runfile "$5")"
package_name="$6"
systemd_service="$7"
systemd_account_user="$8"
systemd_account_group="$9"
systemd_account_home="${10}"
systemd_account_shell="${11}"
systemd_state_directory="${12}"
systemd_state_mode="${13}"
expected_file_count="${14}"
shift 14

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required to run package installation tests" >&2
  exit 1
fi
docker info >/dev/null

docker pull --platform "${docker_platform}" "${image}"

container_id="$(
  docker create \
    --platform "${docker_platform}" \
    "${image}" \
    /bin/sh \
    /tmp/package-install-container.sh \
    "${package_format}" \
    "${package_name}" \
    "${systemd_service}" \
    "${systemd_account_user}" \
    "${systemd_account_group}" \
    "${systemd_account_home}" \
    "${systemd_account_shell}" \
    "${systemd_state_directory}" \
    "${systemd_state_mode}" \
    "${expected_file_count}" \
    "$@"
)"
trap 'docker rm --force "${container_id}" >/dev/null 2>&1 || true' EXIT

docker cp --follow-link "${package_path}" "${container_id}:/tmp/package.${package_format}"
docker cp --follow-link "${container_script}" "${container_id}:/tmp/package-install-container.sh"
docker start --attach "${container_id}"
