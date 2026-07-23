#!/usr/bin/env bash
set -Eeuo pipefail

: "${META_REPOSITORY:?META_REPOSITORY is required}"
: "${META_BRANCH:?META_BRANCH is required}"
: "${UPSTREAM_REPOSITORY:?UPSTREAM_REPOSITORY is required}"
: "${UPSTREAM_BRANCH:?UPSTREAM_BRANCH is required}"
: "${PUBLISH_REPOSITORY:?PUBLISH_REPOSITORY is required}"
: "${INTEGRATION_BRANCH:?INTEGRATION_BRANCH is required}"
: "${INITIAL_UPSTREAM_CUTOFF:?INITIAL_UPSTREAM_CUTOFF is required}"
: "${GH_TOKEN:?GH_TOKEN is required}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
: "${GITHUB_STEP_SUMMARY:?GITHUB_STEP_SUMMARY is required}"
: "${GITHUB_WORKSPACE:?GITHUB_WORKSPACE is required}"
: "${RUNNER_TEMP:?RUNNER_TEMP is required}"

exec > >(tee "${GITHUB_WORKSPACE}/port-debug.log") 2>&1

git config --global user.name "github-actions[bot]"
git config --global user.email "41898282+github-actions[bot]@users.noreply.github.com"

work_dir="${RUNNER_TEMP}/sing-tun-port"
applied_file="${RUNNER_TEMP}/sing-tun-applied.tsv"
skipped_file="${RUNNER_TEMP}/sing-tun-skipped.tsv"
rm -rf "$work_dir"
: > "$applied_file"
: > "$skipped_file"

git clone --no-tags --branch "$META_BRANCH" \
  "https://github.com/${META_REPOSITORY}.git" "$work_dir"
cd "$work_dir"

git remote rename origin meta
git remote add upstream "https://github.com/${UPSTREAM_REPOSITORY}.git"
git fetch --no-tags upstream \
  "+refs/heads/${UPSTREAM_BRANCH}:refs/remotes/upstream/${UPSTREAM_BRANCH}"

publish_url="https://x-access-token:${GH_TOKEN}@github.com/${PUBLISH_REPOSITORY}.git"
git remote add publish "$publish_url"

integration_base_sha=""
if git ls-remote --exit-code publish "refs/heads/${INTEGRATION_BRANCH}" >/dev/null 2>&1; then
  git fetch --no-tags publish \
    "+refs/heads/${INTEGRATION_BRANCH}:refs/remotes/publish/${INTEGRATION_BRANCH}"
  integration_base_sha="$(git rev-parse "refs/remotes/publish/${INTEGRATION_BRANCH}")"
fi

# Always rebuild from the latest Meta tree. This keeps MetaCubeX's module path,
# dependency graph, and implementation authoritative instead of accumulating a
# second long-lived fork history.
git checkout -B "$INTEGRATION_BRANCH" "refs/remotes/meta/${META_BRANCH}"
meta_sha="$(git rev-parse HEAD)"
upstream_sha="$(git rev-parse "refs/remotes/upstream/${UPSTREAM_BRANCH}")"
cutoff_sha="$(git rev-list -1 \
  --before="$INITIAL_UPSTREAM_CUTOFF" \
  "refs/remotes/upstream/${UPSTREAM_BRANCH}")"
test -n "$cutoff_sha"
git merge-base --is-ancestor "$cutoff_sha" "refs/remotes/upstream/${UPSTREAM_BRANCH}"

mapfile -t candidates < <(git rev-list --reverse --no-merges \
  "${cutoff_sha}..refs/remotes/upstream/${UPSTREAM_BRANCH}")

normalize_imports() {
  local file
  while IFS= read -r -d '' file; do
    sed -i \
      -e 's#github.com/sagernet/sing-tun#github.com/metacubex/sing-tun#g' \
      -e 's#github.com/sagernet/sing#github.com/metacubex/sing#g' \
      -e 's#github.com/sagernet/fswatch#github.com/metacubex/fswatch#g' \
      -e 's#github.com/sagernet/gvisor#github.com/metacubex/gvisor#g' \
      -e 's#github.com/sagernet/nftables#github.com/metacubex/nftables#g' \
      "$file"
  done < <(git grep -Ilz 'github.com/sagernet/' -- '*.go' || true)
}

reject_candidate() {
  local base=$1
  local commit=$2
  local reason=$3
  local subject=$4
  git reset --hard "$base"
  git clean -fd
  printf '%s\t%s\t%s\n' "$commit" "$reason" "$subject" >> "$skipped_file"
}

for commit in "${candidates[@]}"; do
  base="$(git rev-parse HEAD)"
  subject="$(git show -s --format=%s "$commit")"
  echo "Examining ${commit}: ${subject}"

  if ! git cherry-pick --no-commit "$commit"; then
    reject_candidate "$base" "$commit" conflict "$subject"
    continue
  fi

  # A source patch may be adopted, but SagerNet's v0.8 module graph must not
  # replace the MetaCubeX v0.5 graph used by Mihomo.
  git restore --source="$base" --staged --worktree -- go.mod go.sum
  normalize_imports

  if git diff --quiet && git diff --cached --quiet; then
    reject_candidate "$base" "$commit" already-present "$subject"
    continue
  fi

  if ! git diff --check; then
    reject_candidate "$base" "$commit" diff-check "$subject"
    continue
  fi

  test_log="${RUNNER_TEMP}/sing-tun-${commit}.log"
  if ! go test . -run '^$' -count=1 >"$test_log" 2>&1; then
    tail -n 80 "$test_log" || true
    reject_candidate "$base" "$commit" compile "$subject"
    continue
  fi

  if ! go test . -run '^$' -count=1 -tags with_gvisor >>"$test_log" 2>&1; then
    tail -n 80 "$test_log" || true
    reject_candidate "$base" "$commit" gvisor-compile "$subject"
    continue
  fi

  git add -A
  git commit \
    -m "port: ${subject}" \
    -m "Upstream-SagerNet: ${commit}"
  printf '%s\t%s\n' "$commit" "$subject" >> "$applied_file"
done

printf '%s\n' "$upstream_sha" > .sagernet-watermark
{
  echo "# SagerNet compatibility port report"
  echo
  echo "- Meta base: \`${meta_sha}\`"
  echo "- SagerNet examined through: \`${upstream_sha}\`"
  echo "- Cutoff commit: \`${cutoff_sha}\`"
  echo "- Policy: MetaCubeX remains the implementation and module graph; only source patches that compile with and without gVisor are retained."
  echo
  echo "## Applied"
  if [[ -s "$applied_file" ]]; then
    while IFS=$'\t' read -r sha subject; do
      echo "- \`${sha}\` — ${subject}"
    done < "$applied_file"
  else
    echo "- None"
  fi
  echo
  echo "## Skipped"
  if [[ -s "$skipped_file" ]]; then
    while IFS=$'\t' read -r sha reason subject; do
      echo "- \`${sha}\` — **${reason}** — ${subject}"
    done < "$skipped_file"
  else
    echo "- None"
  fi
} > .sagernet-port-report.md

# The integration ref is a Go module snapshot, not another Actions project.
# Omitting workflow files also avoids requiring the GitHub App's workflows
# permission when it publishes this independently rooted branch.
rm -rf .github/workflows

go mod tidy
git add -A

go mod download
go mod verify
go test . -run '^$' -count=1
go test . -run '^$' -count=1 -tags with_gvisor

latest_remote_sha="$(git ls-remote publish "refs/heads/${INTEGRATION_BRANCH}" | awk 'NR == 1 { print $1 }')"
if [[ "$latest_remote_sha" != "$integration_base_sha" ]]; then
  echo "Integration branch moved from ${integration_base_sha:-<none>} to ${latest_remote_sha:-<none>}; refusing to overwrite it." >&2
  exit 1
fi

tree_sha="$(git write-tree)"
if [[ -n "$integration_base_sha" ]] && \
   [[ "$tree_sha" == "$(git rev-parse "${integration_base_sha}^{tree}")" ]]; then
  integration_sha="$integration_base_sha"
  echo "Integration tree is unchanged; no branch update required."
else
  if [[ -n "$integration_base_sha" ]]; then
    integration_sha="$(printf '%s\n' \
      "chore: rebuild Meta sing-tun with compatible SagerNet patches" | \
      git commit-tree "$tree_sha" -p "$integration_base_sha")"
  else
    integration_sha="$(printf '%s\n' \
      "chore: build Meta sing-tun with compatible SagerNet patches" | \
      git commit-tree "$tree_sha")"
  fi
  git reset --hard "$integration_sha"
  git push publish "${integration_sha}:refs/heads/${INTEGRATION_BRANCH}"
fi

commit_epoch="$(git show -s --format=%ct "$integration_sha")"
commit_time="$(date -u -d "@${commit_epoch}" +%Y%m%d%H%M%S)"
integration_version="v0.0.0-${commit_time}-${integration_sha:0:12}"
applied_count="$(wc -l < "$applied_file" | tr -d '[:space:]')"
skipped_count="$(wc -l < "$skipped_file" | tr -d '[:space:]')"

echo "integration_sha=$integration_sha" >> "$GITHUB_OUTPUT"
echo "integration_version=$integration_version" >> "$GITHUB_OUTPUT"
echo "applied_count=$applied_count" >> "$GITHUB_OUTPUT"
echo "skipped_count=$skipped_count" >> "$GITHUB_OUTPUT"

{
  echo "### sing-tun Meta compatibility port"
  echo "- Meta head: \`${meta_sha}\`"
  echo "- SagerNet head: \`${upstream_sha}\`"
  echo "- Integration commit: \`${integration_sha}\`"
  echo "- Replacement version: \`${integration_version}\`"
  echo "- Applied commits: \`${applied_count}\`"
  echo "- Skipped commits: \`${skipped_count}\`"
} >> "$GITHUB_STEP_SUMMARY"
