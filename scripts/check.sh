#!/usr/bin/env bash
# Ground truth for project state. Trust this over any conversation summary.
#
# Exit 0 = everything present is healthy.
# Exit 1 = something is broken or a prerequisite is missing.
set -uo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/env.sh
. scripts/env.sh
FAIL=0

say()  { printf '%s\n' "$*"; }
ok()   { printf '  OK    %s\n' "$*"; }
bad()  { printf '  FAIL  %s\n' "$*"; FAIL=1; }
warn() { printf '  WARN  %s\n' "$*"; }

say "=== toolchain ==="
if command -v go >/dev/null 2>&1; then
  ok "go $(go version | awk '{print $3}')"
else
  bad "go is not installed - no plan task can be built or tested"
fi
if command -v git >/dev/null 2>&1; then ok "git present"; else bad "git missing"; fi

say ""
say "=== secrets must never be tracked ==="
for f in github_token_admin.txt mobinhost_server_1.txt; do
  if git ls-files --error-unmatch "$f" >/dev/null 2>&1; then
    bad "$f IS TRACKED BY GIT - remove it from the index immediately"
  else
    ok "$f untracked"
  fi
done
if grep -rqE "ghp_[A-Za-z0-9]{20,}" --include="*.go" --include="*.md" --include="*.sh" . 2>/dev/null; then
  bad "a GitHub token literal appears in tracked source"
else
  ok "no token literals in source"
fi

say ""
say "=== go modules ==="
MODULES=$(find . -name go.mod -not -path "./.git/*" 2>/dev/null | sed "s|/go.mod\$||")
if [ -z "$MODULES" ]; then
  warn "no Go modules yet - plan Task 1 has not landed"
else
  for m in $MODULES; do
    if ! command -v go >/dev/null 2>&1; then
      warn "skipping $m - go not installed"
      continue
    fi
    # A module with no .go files anywhere is scaffolding waiting for its
    # first task; that is not a failure.
    if [ -z "$(find "$m" -name '*.go' -not -path '*/.git/*' 2>/dev/null | head -1)" ]; then
      warn "$m has no source yet"
      continue
    fi
    if (cd "$m" && go build ./... >/dev/null 2>&1); then ok "build $m"; else bad "build $m"; fi
    if (cd "$m" && go vet ./... >/dev/null 2>&1); then ok "vet   $m"; else bad "vet   $m"; fi
    if (cd "$m" && go test ./... >/dev/null 2>&1); then ok "test  $m"; else bad "test  $m"; fi
  done
fi

say ""
say "=== front end ==="
# Go tests read the JS as text and cannot tell whether it parses. A single
# stray brace in app.js gives the user a blank window, and every Go test still
# passes. node only parses here; it is not a build dependency, so its absence
# is a note rather than a failure.
if ! command -v node >/dev/null 2>&1; then
  warn "skipping the front end - node not installed"
else
  for f in lobbyapp/ui/*.js; do
    [ -e "$f" ] || continue
    if node --check "$f" >/dev/null 2>&1; then ok "parse $f"; else bad "parse $f"; fi
  done
  for f in lobbyapp/ui/strings/*.json; do
    [ -e "$f" ] || continue
    if node -e "JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'))" "$f" >/dev/null 2>&1
    then ok "parse $f"; else bad "parse $f"; fi
  done
fi

say ""
say "=== desktop shell ==="
# The Tauri window (D45, D55). It is checked rather than built: a full build
# links a webview and takes minutes, and what breaks in practice is the Rust,
# not the linker. Its toolchain is not required to work on this project - most
# tasks never touch it - so its absence is a note, not a failure.
if [ ! -d desktop ]; then
  warn "no desktop shell yet - plan Task 11 has not landed"
elif ! command -v cargo >/dev/null 2>&1; then
  warn "skipping the desktop shell - cargo not installed"
else
  if (cd desktop && cargo check --quiet >/dev/null 2>&1); then
    ok "check desktop"
  else
    bad "check desktop"
  fi
fi

say ""
say "=== working tree ==="
if [ -n "$(git status --porcelain)" ]; then
  warn "uncommitted changes present:"
  git status --short | sed "s/^/        /"
else
  ok "clean"
fi
say "  HEAD: $(git log --oneline -1)"

say ""
if [ "$FAIL" -eq 0 ]; then
  say "RESULT: healthy"
  say "        (unit level only. scripts/smoke.sh walks a real coordinator"
  say "         and a real app through signing up and hosting a room.)"
else
  say "RESULT: PROBLEMS FOUND - fix before continuing"
fi
exit "$FAIL"
