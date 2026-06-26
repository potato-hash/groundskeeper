#!/usr/bin/env bash
# Dispatch and watch the clean macOS public install smoke workflow.

set -euo pipefail

REPO="${GK_SMOKE_REPO:-potato-hash/groundskeeper}"
REF="${GK_SMOKE_REF:-main}"
WORKFLOW="${GK_SMOKE_WORKFLOW:-public-install-smoke.yml}"
dispatch_id="gk-smoke-$(date +%s)-$$"
expected_title="public-install-smoke $REF $dispatch_id"

fail() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

command -v gh >/dev/null 2>&1 || fail "GitHub CLI not found; install gh and authenticate first"
gh auth status --hostname github.com >/dev/null 2>&1 || fail "gh is not authenticated for github.com"
[[ "$REF" == "main" ]] || fail "secret-backed public smoke only runs from main; got ref $REF"

secret_names="$(gh secret list --repo "$REPO" | awk '{print $1}')" || fail "could not list repo secrets for $REPO"
if ! grep -Fxq OLLAMA_CLOUD_API_KEY <<< "$secret_names"; then
  fail "missing repo secret OLLAMA_CLOUD_API_KEY; set it with: gh secret set OLLAMA_CLOUD_API_KEY --repo $REPO"
fi

printf '[INFO] Dispatching %s for %s at %s\n' "$WORKFLOW" "$REPO" "$REF"
gh workflow run "$WORKFLOW" --repo "$REPO" --ref "$REF" -f "dispatch_id=$dispatch_id"

run_id=""
for _ in {1..20}; do
  run_id="$(gh run list --repo "$REPO" --workflow "$WORKFLOW" --limit 20 --json databaseId,displayTitle,event,headBranch --jq ".[] | select(.displayTitle == \"$expected_title\" and .event == \"workflow_dispatch\" and .headBranch == \"$REF\") | .databaseId" | head -n 1)"
  if [[ -n "$run_id" ]]; then
    break
  fi
  sleep 3
done

[[ -n "$run_id" ]] || fail "workflow dispatch $dispatch_id did not appear in GitHub Actions"

printf '[INFO] Watching run %s\n' "$run_id"
gh run watch "$run_id" --repo "$REPO" --exit-status
gh run view "$run_id" --repo "$REPO" --json url,conclusion --jq '"[INFO] " + .url + " " + (.conclusion // "")'
