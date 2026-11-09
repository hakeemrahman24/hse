#!/usr/bin/env bash
# Push the current branch to GitHub (hakeemrahman24/hse).
#
# The Git repo's root is ~/Projects, but the GitHub repo "hse" holds only the hse/ folder, so a plain
# `git push` cannot work: the hashes differ. `git subtree split --prefix=hse` rebuilds the hse/ history
# deterministically (the same commits get the same hashes every time), so each push only sends what is new.
# Never forces. Run it after every commit and after every deploy (see CLAUDE.md).
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
branch="$(git rev-parse --abbrev-ref HEAD)"
if ! git remote get-url origin >/dev/null 2>&1; then
  echo "abort: no 'origin' remote. Add it: git remote add origin https://github.com/hakeemrahman24/hse.git" >&2
  exit 1
fi
tip="$(git subtree split --prefix=hse HEAD 2>/dev/null | tail -1 | grep -o '[0-9a-f]\{40\}$')"
echo "pushing hse/ history of $branch (tip $tip) to origin/$branch"
git push origin "${tip}:refs/heads/${branch}"
