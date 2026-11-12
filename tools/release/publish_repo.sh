#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# One-time publication of Lamp's public repository from one redacted commit.
#   tools/release/publish_repo.sh PATTERNS_FILE [--go]
# Without --go it only prints what it would do. With --go it:
#   1. renames the private JohanSardar/lamp to JohanSardar/lamp-dev;
#   2. points `origin` of every local checkout at lamp-dev;
#   3. exports origin/main, redacts it, refuses on any remaining match,
#      and pushes it as the single commit of a new public JohanSardar/lamp.
set -eu
patterns=${1:?usage: publish_repo.sh PATTERNS_FILE [--go]}
go=${2:-}
owner=JohanSardar
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
run() { echo "+ $*"; if [ "$go" = --go ]; then "$@"; fi; }

[ "$(gh repo view $owner/lamp --json visibility -q .visibility)" = PRIVATE ] || { echo "JohanSardar/lamp is not the private repo"; exit 1; }
[ -z "$(gh api repos/$owner/lamp/actions/runners -q ".runners[].name")" ] || { echo "retire the self-hosted runners first"; [ "$go" = --go ] && exit 1; }

run gh repo rename lamp-dev --repo $owner/lamp --yes
for d in "$HOME/.t3/projects/lamp" "$HOME"/.t3/worktrees/lamp/*; do
  [ -e "$d/.git" ] || continue
  case "$(git -C "$d" remote get-url origin 2>/dev/null)" in
    *"$owner/lamp.git"|*"$owner/lamp") run git -C "$d" remote set-url origin "https://github.com/$owner/lamp-dev.git" ;;
  esac
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git -C "$root" fetch -q origin main
git -C "$root" archive origin/main | tar -x -C "$work"
cd "$work"
git init -q -b main
python3 "$root/tools/release/redact.py" --patterns "$patterns"
git add -A
python3 "$root/tools/release/redact.py" --patterns "$patterns" --check
git -c user.name="$owner" -c user.email="68117503+$owner@users.noreply.github.com" commit -qm "Lamp 0.1.0"
run gh repo create $owner/lamp --public --description "Fast, isolated sandboxes for agents, harnesses and CI on your own machines" --homepage https://eilmer.si
run git push "https://github.com/$owner/lamp.git" main
