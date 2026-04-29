#!/usr/bin/env bash
set -euo pipefail

if ! command -v gh >/dev/null 2>&1; then
  echo "gh is required"
  exit 1
fi

VERSION="${1:-}"
if [[ -z "${VERSION}" ]]; then
  echo "usage: $0 <version-without-prefix>"
  echo "example: $0 0.1.0"
  exit 1
fi

REPO="${2:-}"
if [[ -z "${REPO}" ]]; then
  REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
fi

echo "Triggering dry-run workflows for ${REPO} version ${VERSION}"

dispatch() {
  local workflow="$1"
  echo "--- dispatch ${workflow} ---"
  gh workflow run "$workflow" \
    --repo "$REPO" \
    -f version="$VERSION" \
    -f push_artifacts=false \
    -f force_release=true
  return 0
}

dispatch ".github/workflows/release-operator.yml"
dispatch ".github/workflows/release-olm.yml"
dispatch ".github/workflows/release-helm.yml"

echo "Dry-run dispatch complete"
echo "Check runs with: gh run list --repo ${REPO} --limit 20"
