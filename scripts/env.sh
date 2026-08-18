#!/usr/bin/env bash
# Shared environment for every script here. Source it; do not execute it.
#
# Why this exists: the Go toolchain is installed per-user rather than
# system-wide (no admin rights were needed), and a shell that was started
# before the install will not have it on PATH. Rather than depend on a
# correctly configured shell, every script finds Go for itself.

# --- Go toolchain ------------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
  for candidate in \
    "$HOME/sdk/go/bin" \
    "/c/Users/$USERNAME/sdk/go/bin" \
    "/c/Program Files/Go/bin" \
    "/usr/local/go/bin"
  do
    if [ -x "$candidate/go" ] || [ -x "$candidate/go.exe" ]; then
      PATH="$candidate:$PATH"
      export PATH
      break
    fi
  done
fi

# --- Module proxy ------------------------------------------------------
# proxy.golang.org and dl.google.com are unreachable from Iranian networks
# (verified 2026-08-18: connection blackholed and a synthetic 404 respectively).
# goproxy.cn serves modules and proxies checksum-database lookups, so module
# verification still happens - this is a reachability workaround, not a
# security downgrade.
: "${GOPROXY:=https://goproxy.cn,direct}"
export GOPROXY

# Never let the toolchain try to download a different Go version over a
# blocked link; fail loudly on a version mismatch instead.
: "${GOTOOLCHAIN:=local}"
export GOTOOLCHAIN
