#!/usr/bin/env bash

set -euo pipefail

for image in "$@"; do
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
for image in "$@"; do
  pusher="${image%%=*}"
  repository="${image#*=}"
  "$pusher" --repository "$registry/$repository"
done
