#!/usr/bin/env bash
# Merge a Windshiftapp/core ref into a new branch off the fork's main, push it
# to Forgejo (origin), and open a pull request there. The Forgejo push mirror
# carries the merged result to GitHub; never sync the GitHub fork directly.
#
# Usage:
#   pienter/sync-upstream.sh [REF] [KEY]
#
#   REF  upstream branch or tag to merge (default: main)
#   KEY  Windshift item key, e.g. WCORE-20; names the branch feature/<KEY>-...
#        and adds a Refs: footer to the merge commit
set -euo pipefail

ref="${1:-main}"
key="${2:-}"
upstream_url="https://github.com/Windshiftapp/core.git"

if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
  echo "Error: commit or stash your changes first." >&2
  exit 1
fi

if ! git remote get-url upstream >/dev/null 2>&1; then
  git remote add upstream "$upstream_url"
  # Fetch-only: nothing from the fork is ever pushed to upstream.
  git remote set-url --push upstream DISABLED
fi

git fetch --quiet origin main
git fetch --quiet --tags upstream

if target=$(git rev-parse --verify --quiet "upstream/$ref^{commit}"); then
  :
elif target=$(git rev-parse --verify --quiet "refs/tags/$ref^{commit}"); then
  :
else
  echo "Error: '$ref' is neither an upstream branch nor a tag." >&2
  exit 1
fi
short=$(git rev-parse --short "$target")

if git merge-base --is-ancestor "$target" origin/main; then
  echo "Fork main already contains upstream $ref ($short)."
  exit 0
fi
count=$(git rev-list --count "origin/main..$target")

slug="sync-upstream-${ref//\//-}"
if [[ -n "$key" ]]; then
  branch="feature/$key-$slug"
else
  branch="sync/${slug#sync-}-$(date +%Y%m%d)"
fi
if git show-ref --verify --quiet "refs/heads/$branch"; then
  echo "Error: branch $branch already exists." >&2
  exit 1
fi

git switch --quiet -c "$branch" origin/main
git branch --quiet --unset-upstream

title="Sync upstream Windshiftapp/core $ref ($short)"
message="$title"
[[ -n "$key" ]] && message+=$'\n\n'"Refs: $key"

if ! git merge --no-edit -m "$message" "$target"; then
  cat >&2 <<EOF

Merge conflicts on $branch. Resolve them, then:
  git commit --no-edit
  git push -u origin HEAD
and open the pull request on Forgejo.
EOF
  exit 1
fi

git push --quiet -u origin HEAD
echo "Merged $count upstream commit(s) from $ref ($short) into $branch."

body="Merges upstream \`Windshiftapp/core\` \`$ref\` at \`$short\` ($count commits) into fork \`main\`."
[[ -n "$key" ]] && body+=$'\n\n'"Refs: $key"
if command -v fj >/dev/null 2>&1; then
  fj pr create --base main --head "$branch" --body "$body" "$title"
else
  echo "Open the pull request for $branch on Forgejo (fj not installed)."
fi
