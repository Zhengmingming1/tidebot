#!/usr/bin/env bash
# Install apt packages on GitHub-hosted Ubuntu runners without hanging on a slow mirror.
# The Azure Ubuntu mirror sometimes trickles at ~40 KB/s, which turned a 30 s step into
# a 20 min job timeout. Downloads go to a directory the workflow caches between runs
# (APT_ARCHIVE_DIR), every request has a short timeout, and if the install still stalls
# it is retried once against archive.ubuntu.com.
# Usage: scripts/ci-apt-install.sh [apt-get install options] package...
set -euo pipefail

cache="${APT_ARCHIVE_DIR:-$HOME/.cache/apt-archives}"
mkdir -p "$cache/partial"
net=(-o Acquire::Retries=3 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20)

attempt() {
  local limit="$1"; shift
  sudo apt-get update -q "${net[@]}" &&
    sudo timeout "$limit" apt-get install -y -q "${net[@]}" -o "Dir::Cache::Archives=$cache" "$@"
}

if ! attempt 420 "$@"; then
  echo "::warning::apt install stalled or failed on the default mirror; retrying via archive.ubuntu.com"
  for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources; do
    [ -f "$f" ] || continue
    sudo sed -i -E 's#mirror\+file:/etc/apt/apt-mirrors\.txt#http://archive.ubuntu.com/ubuntu/#g; s#https?://azure\.archive\.ubuntu\.com/ubuntu/?#http://archive.ubuntu.com/ubuntu/#g' "$f"
  done
  attempt 600 "$@"
fi

# actions/cache runs as the runner user; apt left root-owned files behind.
sudo rm -f "$cache/lock"
sudo rm -rf "$cache/partial"
sudo chown -R "$(id -u):$(id -g)" "$cache"
