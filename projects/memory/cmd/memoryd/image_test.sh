#!/usr/bin/env bash
set -euo pipefail
archive="$1"
arch="$2"
probe="$3"
image="local/memory:$arch"
docker load --input "$archive" >/dev/null
# The final distroless image executes as the caller's numeric nonroot identity,
# with a read-only root and no network. Probes never require curl or a shell.
test "$(id -u)" != 0 || probe_uid=65532
probe_uid="${probe_uid:-$(id -u)}"
docker run --rm --network none --read-only --user "$probe_uid:$(id -g)" \
  --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 --memory 256m \
  "$image" version --json
socket_dir="$(mktemp -d /tmp/memory-image.XXXXXXXX)"
chmod 750 "$socket_dir"
cid=""
cleanup() {
  if [[ -n "$cid" ]]; then docker rm --force "$cid" >/dev/null; fi
  rm -rf "$socket_dir"
}
trap cleanup EXIT
# Docker's managed daemon sees this workspace's /tmp bind mounts. The worker
# receives only its IPC directory and peer UID; no DB/auth/build credentials.
if [[ "$(id -u)" != 0 ]]; then
  cid="$(docker run --detach --network none --read-only --user "$(id -u):$(id -g)" \
    --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 --memory 256m \
    --mount "type=bind,src=$socket_dir,dst=/ipc" \
    --env MEMORY_WORKER_SOCKET=/ipc/worker.sock --env MEMORY_PEER_UID="$(id -u)" \
    "$image" worker)"
  ready=false
  for attempt in {1..40}; do
    if docker exec "$cid" /memoryd healthcheck worker; then ready=true; break; fi
    sleep 0.1
  done
  test "$ready" = true
  test "$(docker inspect --format '{{.HostConfig.NetworkMode}} {{.HostConfig.ReadonlyRootfs}}' "$cid")" = "none true"
  "$probe" "$socket_dir/worker.sock" good
  "$probe" "$socket_dir/worker.sock" timeout
  "$probe" "$socket_dir/worker.sock" good
  "$probe" "$socket_dir/worker.sock" oom
  for attempt in {1..40}; do
    if [[ "$(docker inspect --format '{{.State.Running}}' "$cid")" == false ]]; then break; fi
    sleep 0.1
  done
  test "$(docker inspect --format '{{.State.OOMKilled}}' "$cid")" = true
  docker rm --force "$cid" >/dev/null
  cid="$(docker run --detach --network none --read-only --user "$(id -u):$(id -g)" \
    --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 --memory 256m \
    --mount "type=bind,src=$socket_dir,dst=/ipc" \
    --env MEMORY_WORKER_SOCKET=/ipc/worker.sock --env MEMORY_PEER_UID="$(id -u)" \
    "$image" worker)"
  ready=false
  for attempt in {1..40}; do
    if docker exec "$cid" /memoryd healthcheck worker; then ready=true; break; fi
    sleep 0.1
  done
  test "$ready" = true
  "$probe" "$socket_dir/worker.sock" good
fi
