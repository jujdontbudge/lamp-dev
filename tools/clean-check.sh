#!/bin/sh
# Run inside a disposable clean source checkout, never the user's working tree.
set -eu
printf 'UTC: '; date -u '+%Y-%m-%dT%H:%M:%SZ'
uname -sm
go version
rustc --version
cargo --version
python3 --version
if test -n "$(git status --porcelain)"; then
  echo 'FAIL: source checkout starts dirty' >&2
  exit 1
fi
printf 'Source snapshot: '; git rev-parse HEAD
make bootstrap verify
# A regeneration check must not mutate sources.
if test -n "$(git status --porcelain)"; then
  git status --short
  echo 'FAIL: source checkout changed during verification' >&2
  exit 1
fi
printf 'PASS: clean checkout build/test/contracts/interop/lint on '
uname -sm
