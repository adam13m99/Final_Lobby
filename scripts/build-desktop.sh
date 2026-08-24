#!/usr/bin/env bash
# Builds the desktop shell (D45, D55).
#
# The shell is a window, a tray icon and notifications; everything the product
# actually does is in the Go binary it launches. So this builds that binary
# first, puts it where the shell expects it, and then builds the shell.
#
# This is deliberately NOT wired into scripts/publish.sh yet. publish.sh is the
# owner's only distribution channel and the installer it produces is known to
# work; swapping it for one nobody has installed on a real machine would risk
# the thing that currently works, to ship the thing that is not yet proven. Try
# the shell by hand first, then wire it in.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/env.sh

TARGET=x86_64-pc-windows-msvc

echo "building the Go client..."
mkdir -p desktop/binaries
( cd lobbyapp && go build -o "../desktop/binaries/lobbyapp-$TARGET.exe" . )
# Tauri's externalBin wants the target suffix; running from target/release
# wants the bare name. Both, so a developer run and a bundle both work.
cp "desktop/binaries/lobbyapp-$TARGET.exe" desktop/binaries/lobbyapp.exe

echo "building the desktop shell..."
( cd desktop && cargo build --release )
cp desktop/binaries/lobbyapp.exe desktop/target/release/lobbyapp.exe

echo
echo "  desktop/target/release/lobbybaz.exe"
echo
echo "Run it from that directory - it looks for lobbyapp.exe beside itself."
