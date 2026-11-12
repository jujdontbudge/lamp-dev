#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# One-time publication of Lamp's public GitLab project from one redacted commit.
#   tools/release/publish_repo.sh PATTERNS_FILE REF RELEASE_DIR [--go]
# REF is the reviewed development commit; RELEASE_DIR is tools/release/build.py's output.
#
# Order: preflight -> export REF -> redact -> check -> secret-pattern scan, all in
# a temporary directory. Without --go it stops there and changes nothing. With
# --go, one step at a time with pauses (no bursts): create the private project
# gitlab.com/johansardar/lamp, upload RELEASE_DIR to its generic package
# registry, push the one commit, protect
# every branch so no one can push or merge, tag and release v<version>, and make
# the project public. lamp.eilmer.si is a Cloudflare Worker (tools/release/deploy_worker.sh).
set -eu
patterns=${1:?usage: publish_repo.sh PATTERNS_FILE REF RELEASE_DIR [--go]}
ref=${2:?usage: publish_repo.sh PATTERNS_FILE REF RELEASE_DIR [--go]}
rel=${3:?usage: publish_repo.sh PATTERNS_FILE REF RELEASE_DIR [--go]}
go=${4:-}
url=https://gitlab.com/johansardar/lamp
project=${url#https://gitlab.com/}
api=projects/$(printf %s "$project" | sed s#/#%2F#)
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
die() { echo "publish_repo: $*" >&2; exit 1; }
pause() { sleep 20; }

version=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["version"])' "$rel/release.json")
(cd "$rel" && shasum -a 256 -c SHA256SUMS >/dev/null) || die "release files do not match SHA256SUMS"
if glab api "$api" >/dev/null 2>&1; then die "$project already exists"; fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git -C "$root" archive "$ref" | tar -x -C "$work"
git -C "$work" init -q -b main
git -C "$work" add -A
python3 "$root/tools/release/redact.py" --root "$work" --patterns "$patterns"
git -C "$work" add -A
python3 "$root/tools/release/redact.py" --root "$work" --patterns "$patterns" --check || die "redaction check failed; nothing was changed"
# Credential formats; every hit must be a known test fixture, listed by path.
hits=$(git -C "$work" grep -lE -e '-----BEGIN [A-Z ]*PRIVATE KEY-----' -e 'glpat-[0-9A-Za-z_-]{20}' \
  -e '(ghp|gho|ghs|ghu)_[0-9A-Za-z]{36}' -e 'github_pat_[0-9A-Za-z_]{40}' -e 'sk-(ant|or|proj)-[0-9A-Za-z_-]{20}' \
  -e 'AKIA[0-9A-Z]{16}' -e 'xox[abpr]-[0-9A-Za-z-]{10}' -e 'AIza[0-9A-Za-z_-]{35}' -e 'sk_(live|test)_[0-9A-Za-z]{20}' || true)
allowed=$(cat "$root/tools/release/secret-fixtures.txt")
for h in $hits; do
  printf '%s\n' "$allowed" | grep -qxF "$h" || die "credential-shaped text in $h; not publishing"
done
git -C "$work" -c user.name=Lamp -c user.email=noreply@lamp.eilmer.si commit -qm "Lamp $version"
echo "redacted export: $(git -C "$work" ls-files | wc -l | tr -d ' ') files, commit $(git -C "$work" rev-parse --short HEAD), $(printf '%s\n' $hits | grep -c . || true) known test fixtures"
[ "$go" = --go ] || { echo "dry run: nothing was changed"; exit 0; }

glab api -X POST projects -f name=lamp -f path=lamp -f visibility=private \
  -f description="Fast, isolated sandboxes for agents, harnesses and CI on your own machines" >/dev/null
echo "created $project (private)"; pause
# Only SHA256SUMS and the files it lists (checked above) reach the registry.
for name in SHA256SUMS $(awk '{print $2}' "$rel/SHA256SUMS"); do
  f=$rel/$name
  glab api -X PUT "$api/packages/generic/lamp/$version/$(basename "$f")" --input "$f" >/dev/null
  echo "uploaded $(basename "$f")"; sleep 5
done
pause
git -C "$work" push -q "git@gitlab.com:$project.git" main
echo "pushed main"; pause
glab api -X DELETE "$api/protected_branches/main" >/dev/null 2>&1 || true
glab api -X POST "$api/protected_branches" -f name='*' -f push_access_level=0 -f merge_access_level=0 \
  -f allow_force_push=false >/dev/null
echo "protected every branch: no one may push or merge"; pause
base="https://lamp.eilmer.si/releases/v$version"
links=$(cd "$rel" && for f in *; do printf '{"name":"%s","url":"%s/%s","direct_asset_path":"/%s"},' "$f" "$base" "$f" "$f"; done)
printf '{"name":"Lamp %s","tag_name":"v%s","ref":"main","description":%s,"assets":{"links":[%s]}}' \
  "$version" "$version" "$(python3 -c 'import json,sys;print(json.dumps(open(sys.argv[1]).read()))' "$work/docs/release/$version.md")" \
  "${links%,}" | glab api -X POST "$api/releases" -H 'Content-Type: application/json' --input - >/dev/null
echo "released v$version"; pause
glab api -X PUT "$api" -f visibility=public -f pages_access_level=public >/dev/null
echo "published $url"
