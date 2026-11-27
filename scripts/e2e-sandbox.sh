#!/usr/bin/env bash
# Command sandbox end to end: real Claude Code + real bubblewrap + tidebot's
# per-run sandbox settings, driven by a scripted fake Messages API (no model
# key, loopback only). See internal/guard/sandbox_e2e_test.go.
# CI (Linux) runs it after installing bubblewrap and socat. Locally:
#   TIDEBOT_E2E_CLAUDE=$(command -v claude) bash scripts/e2e-sandbox.sh
set -euo pipefail

# Pinned Claude Code build (the version the sandbox settings were verified with).
CC_VERSION=2.1.301
CC_SHA256=a7ffd61e7784e6dda864a31e934cececc050f106015201cb3db4abb59c781c57 # claude-code-linux-x64-2.1.301.tgz

if [ -z "${TIDEBOT_E2E_CLAUDE:-}" ]; then
  [ "$(uname -s)-$(uname -m)" = Linux-x86_64 ] || { echo "set TIDEBOT_E2E_CLAUDE (pinned download is linux-x64 only)" >&2; exit 2; }
  dir="$(mktemp -d)"
  trap 'rm -rf "$dir"' EXIT
  curl -sSfL --retry 3 -o "$dir/cc.tgz" \
    "https://registry.npmjs.org/@anthropic-ai/claude-code-linux-x64/-/claude-code-linux-x64-${CC_VERSION}.tgz"
  echo "${CC_SHA256}  $dir/cc.tgz" | sha256sum -c -
  tar -xzf "$dir/cc.tgz" -C "$dir" package/claude
  TIDEBOT_E2E_CLAUDE="$dir/package/claude"
  chmod +x "$TIDEBOT_E2E_CLAUDE"
fi
"$TIDEBOT_E2E_CLAUDE" --version

# Claude Code's sandbox starts bwrap and, inside it, a second user namespace
# (its seccomp helper), so check both levels.
nested() { bwrap --unshare-user --unshare-net --dev-bind / / bwrap --unshare-user --unshare-net --dev-bind / / true; }

# Ubuntu 24.04 runners refuse unprivileged user namespaces by default (AppArmor):
# the sysctl for bwrap itself, and the bwrap-userns-restrict profile that takes
# capabilities away inside bwrap (no nested namespace). Only on CI: tidebot
# itself never changes kernel or AppArmor settings.
if [ "${CI:-}" = true ]; then
  aa=/proc/sys/kernel/apparmor_restrict_unprivileged_userns
  if [ -r "$aa" ] && [ "$(cat "$aa")" = 1 ]; then
    sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
  fi
  if ! nested; then
    echo "nested user namespace refused inside bwrap; unloading AppArmor's bwrap profile (CI only)"
    for f in /etc/apparmor.d/bwrap-userns-restrict /etc/apparmor.d/unpriv_bwrap; do
      [ -f "$f" ] && sudo apparmor_parser -R "$f" || true
    done
  fi
fi
nested && echo "bwrap: nested user namespace OK"

TIDEBOT_SANDBOX_E2E=1 TIDEBOT_E2E_CLAUDE="$TIDEBOT_E2E_CLAUDE" \
  go test -count=1 -v -run 'TestSandboxEndToEnd' ./internal/guard
