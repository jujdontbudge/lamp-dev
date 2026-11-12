#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# One-time publication of Lamp's public repository from one redacted commit.
#   tools/release/publish_repo.sh PATTERNS_FILE REF [--go]   (REF: the reviewed commit to publish)
#
# Order: preflight -> export REF -> stage -> redact -> check, all in a
# temporary directory. Without --go it stops there: it modifies no checkout,
# remote or repository, and prints the remotes it would rewrite. With --go it
# then renames the private JohanSardar/lamp to lamp-dev, rewrites every remote
# url/pushurl pointing at JohanSardar/lamp in every checkout on the M5, the
# Ubuntu laptop and the Air, creates the public JohanSardar/lamp, pushes the one
# commit, and adds a ruleset that blocks branch creation and pushes for everyone.
set -eu
patterns=${1:?usage: publish_repo.sh PATTERNS_FILE REF [--go]}
ref=${2:?usage: publish_repo.sh PATTERNS_FILE REF [--go]}
go=${3:-}
owner=JohanSardar
hosts=${LAMP_PUBLISH_HOSTS:?set LAMP_PUBLISH_HOSTS to the other machines ssh aliases}
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
die() { echo "publish_repo: $*" >&2; exit 1; }

# Preflight; an API failure is a refusal, never an empty pass.
vis=$(gh repo view $owner/lamp --json visibility -q .visibility) || die "cannot read repository visibility"
[ "$vis" = PRIVATE ] || die "JohanSardar/lamp is not the private repository"
runners=$(gh api repos/$owner/lamp/actions/runners -q '.total_count') || die "cannot list runners"
if [ "$runners" != 0 ]; then
  [ "$go" = --go ] && die "retire the $runners self-hosted runners first"
  echo "note: $runners self-hosted runners still registered (--go refuses until retired)"
fi
if gh repo view $owner/lamp-dev >/dev/null 2>&1; then die "JohanSardar/lamp-dev already exists"; fi

# Export, stage and redact in a temporary directory; check before any effect.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git -C "$root" fetch -q origin
git -C "$root" archive "$ref" | tar -x -C "$work"
git -C "$work" init -q -b main
git -C "$work" add -A
python3 "$root/tools/release/redact.py" --root "$work" --patterns "$patterns"
git -C "$work" add -A
python3 "$root/tools/release/redact.py" --root "$work" --patterns "$patterns" --check || die "redaction check failed; nothing was changed"
git -C "$work" -c user.name="$owner" -c user.email="68117503+$owner@users.noreply.github.com" commit -qm "Lamp 0.1.0"
echo "redacted export: $(git -C "$work" ls-files | wc -l | tr -d ' ') files, commit $(git -C "$work" rev-parse --short HEAD)"

# Every git config under a home directory whose remotes point at JohanSardar/lamp.
rewrite='
mode=$1
find "$HOME" -maxdepth 6 \( -name Library -o -name node_modules -o -name .cache -o -name target -o -name anaconda3 \
  -o -name .cargo -o -name .rustup -o -name go -o -name .npm -o -name .Trash -o -name .local -o -name snap \
  -o -name objects -o -name .venv \) -prune -o -type f -path "*.git/config" -print 2>/dev/null | while read -r c; do
  git config --file "$c" --get-regexp "^remote\..*\.(url|pushurl)$" 2>/dev/null |
  while read -r key url; do
    case "$url" in
      *JohanSardar/lamp|*JohanSardar/lamp.git|*JohanSardar/lamp/)
        new=$(printf %s "$url" | sed -E "s#JohanSardar/lamp(\.git|/)?\$#JohanSardar/lamp-dev.git#")
        echo "$(hostname -s): $c $key -> $new"
        if [ "$mode" = go ]; then git config --file "$c" "$key" "$new"; fi ;;
    esac
  done
done'
echo "remotes to rewrite:"
sh -c "$rewrite" sh dry
for h in $hosts; do ssh -o BatchMode=yes "$h" sh -s dry <<EOF || die "cannot inspect checkouts on $h"
$rewrite
EOF
done
[ "$go" = --go ] || { echo "dry run: nothing was changed"; exit 0; }

gh repo rename lamp-dev --repo $owner/lamp --yes
sh -c "$rewrite" sh go
for h in $hosts; do ssh -o BatchMode=yes "$h" sh -s go <<EOF
$rewrite
EOF
done
gh repo create $owner/lamp --public --description "Fast, isolated sandboxes for agents, harnesses and CI on your own machines"
git -C "$work" push "https://github.com/$owner/lamp.git" main
# No one, admins included, can create branches or push: a missed private
# checkout cannot publish into the public repository.
gh api -X POST repos/$owner/lamp/rulesets --input - <<'EOF'
{"name": "no pushes", "target": "branch", "enforcement": "active", "bypass_actors": [],
 "conditions": {"ref_name": {"include": ["~ALL"], "exclude": []}},
 "rules": [{"type": "creation"}, {"type": "update"}, {"type": "deletion"}, {"type": "non_fast_forward"}]}
EOF
echo "published https://github.com/$owner/lamp"
