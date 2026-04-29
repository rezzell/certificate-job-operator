#!/usr/bin/env bash
set -euo pipefail

REPO="${1:-}"
BRANCH="${2:-main}"
REQUIRED_APPROVING_REVIEW_COUNT="${REQUIRED_APPROVING_REVIEW_COUNT:-0}"

if [[ -z "${REPO}" ]]; then
  REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
fi

if [[ -z "${REPO}" ]]; then
  echo "unable to determine repository (pass as owner/repo)"
  exit 1
fi

if ! [[ "${REQUIRED_APPROVING_REVIEW_COUNT}" =~ ^[0-6]$ ]]; then
  echo "REQUIRED_APPROVING_REVIEW_COUNT must be an integer from 0 to 6"
  exit 1
fi

echo "Applying branch protection to ${REPO}:${BRANCH}"
echo "Required approving review count: ${REQUIRED_APPROVING_REVIEW_COUNT}"

gh api \
  --method PUT \
  -H "Accept: application/vnd.github+json" \
  "/repos/${REPO}/branches/${BRANCH}/protection" \
  --input - <<JSON
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "Lint / Run on Ubuntu",
      "Tests / Run on Ubuntu",
      "E2E Tests / Run on Ubuntu",
      "Security / govulncheck",
      "Security / gosec",
      "SonarCloud / SonarCloud Scan",
      "Workflow Lint / Validate GitHub Workflows"
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": false,
    "required_approving_review_count": ${REQUIRED_APPROVING_REVIEW_COUNT},
    "require_last_push_approval": false
  },
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "block_creations": false,
  "required_conversation_resolution": true,
  "lock_branch": false,
  "allow_fork_syncing": true
}
JSON

echo "Branch protection applied"
