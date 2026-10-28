#!/bin/sh
# Records real GitHub API responses for the fact-source fixtures (R4.3).
# Each fixture is one real public commit, chosen for one fact; the tests
# serve them all as one repository, acme/shop. Diff patches are dropped
# and long lists trimmed to the fields the extension reads; the shapes are
# GitHub's own. Run with an authenticated gh. CI states change over time:
# a commit recorded while its CI was running won't be running later.
set -eu
cd "$(dirname "$0")"
rec() { # name repo sha
  mkdir -p "$1"
  gh api "repos/$2/commits/$3" --jq 'del(.files[]?.patch)' > "$1/commit.json"
  gh api "repos/$2/commits/$3/check-runs?per_page=100" --jq '{total_count, check_runs: [.check_runs[] | {name, status, conclusion}]}' > "$1/check-runs.json"
  gh api "repos/$2/commits/$3/status" --jq '{state, total_count, statuses: [.statuses[] | {context, state}]}' > "$1/status.json"
  gh api "repos/$2/commits/$3/pulls" --jq '[.[] | {number, state, merged_at}]' > "$1/pulls.json"
  pr=$(jq -r '.[0].number // empty' "$1/pulls.json")
  if [ -n "$pr" ]; then
    gh api "repos/$2/pulls/$pr/reviews?per_page=100" --jq '[.[] | {user: {login: .user.login}, state, submitted_at}]' > "$1/reviews.json"
  else
    echo '[]' > "$1/reviews.json"
  fi
  printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> sources.tsv
}
: > sources.tsv
rec ci-pending microsoft/vscode 60d8077cab4490c87787119f8f1b4c9d69f78698
rec ci-red cli/cli 389b60d0b6df69299ccdd36f9a757fe7cf7f5b6a
rec plain-green cli/cli 9e1e1c929f882fd93e1c67ea520a4529352aab9a
rec docs-green cli/cli 5aef01fab5
rec lockfile-green cli/cli 45250aec8ba977669c62384bacaaed290c2460ad
rec migrations-green mastodon/mastodon 7ad82554f8
rec unsigned-no-ci mohsalsaleem/OpenAppPlatform 2115d86
