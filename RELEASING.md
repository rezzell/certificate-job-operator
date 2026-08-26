# Releasing

This repository supports independent release pipelines for:
- Operator image
- OLM bundle/catalog
- Helm chart

You can release any component without releasing the others.

## Versioning and Tags

Tag prefixes map directly to a component release workflow:

- `operator-vX.Y.Z` -> `.github/workflows/release-operator.yml`
- `olm-vX.Y.Z` -> `.github/workflows/release-olm.yml`
- `helm-vX.Y.Z` -> `.github/workflows/release-helm.yml`

Examples:

```bash
git tag operator-v0.2.0
git push origin operator-v0.2.0

git tag olm-v0.2.0
git push origin olm-v0.2.0

git tag helm-v0.2.0
git push origin helm-v0.2.0
```

## Manual Dispatch

All release workflows also support `workflow_dispatch`.

Required/common inputs:
- `version`: semantic version without prefix, for example `0.2.0`
- `push_artifacts`: when `false`, builds/validates without pushing/signing
- `force_release`: bypass component path guards when you intentionally want to release without matching file changes

Component-specific inputs:

- Operator release:
  - `image_repository` (default: `<owner>/certificate-job-operator`)

- OLM release:
  - `operator_image` (optional override)
  - `image_tag_base` (default: `ghcr.io/<owner>/certificate-job-operator`)

- Helm release:
  - `chart_dir` (default: `charts/certificate-job-operator`)
  - `oci_repository` (default: `ghcr.io/<owner>/charts`)

## Path Guards

Each release workflow contains a path guard that auto-skips publishing when no relevant files changed in the tagged commit.

- Operator guard paths include code + runtime/manifests used by the manager image.
- OLM guard paths include bundle/catalog/OLM and generated operator manifest inputs.
- Helm guard paths include `charts/**` and supporting release metadata files.

To bypass path guards, set `force_release=true` in manual dispatch.

## Dry-Run Releases

Before first publish, execute one dry run for each release workflow with:

- `push_artifacts=false`
- `force_release=true` (only if the path guard would otherwise skip)

This validates version parsing, build/scan steps, chart packaging, and generated OLM content without publishing.

Automation helper:

```bash
make release-dry-run VERSION=0.1.0
```

Dry runs do not create GitHub Releases because release records are created only when a workflow runs against a release tag with artifact publishing enabled.

## Recommended Preflight

Run before tagging:

```bash
make generate manifests
go test ./...
make helm-lint
make bundle IMG=ghcr.io/<org>/certificate-job-operator:vX.Y.Z
make catalog-validate
```

Optional full cluster smoke:

```bash
make smoke-e2e
```

## Typical Flows

### 1) Operator image only

Use when controller code/security/runtime changed but OLM and chart should not be published.

```bash
git tag operator-vX.Y.Z
git push origin operator-vX.Y.Z
```

### 2) OLM only

Use when CSV/bundle/catalog content changed.

```bash
git tag olm-vX.Y.Z
git push origin olm-vX.Y.Z
```

### 3) Helm only

Use when Helm templates/values/docs changed for non-OLM users.

```bash
git tag helm-vX.Y.Z
git push origin helm-vX.Y.Z
```

## GitHub Releases

Each release workflow creates or updates a prerelease GitHub Release after the artifact publish/sign steps complete successfully when the workflow runs against a release tag. This includes tag-triggered publishes and manual dispatches using an existing release tag as `--ref` with `push_artifacts=true`.

- Operator releases include the published image tag, digest, immutable image reference, and cosign verification command.
- OLM releases include operator/bundle/catalog image references, cosign verification commands, and an attached `olm-artifacts-vX.Y.Z.tar.gz` archive containing generated `bundle/` and `catalog/` content.
- Helm releases include the OCI chart reference, cosign verification command, and the attached chart `.tgz` package.

Manual `workflow_dispatch` runs from a branch do not create GitHub Releases. If you need a durable release record, run the workflow against the matching release tag so the workflow can prove the tag, publish/sign the artifacts, and then create or update the release.

## Backfilling GitHub Releases

For release tags created after GitHub Release creation was added to the workflows, rerun the matching workflow from the existing tag when possible. The workflow will recreate generated assets from source and then create or update the release:

```bash
gh workflow run .github/workflows/release-operator.yml --ref operator-vX.Y.Z \
  -f version=X.Y.Z \
  -f push_artifacts=true \
  -f force_release=true

gh workflow run .github/workflows/release-olm.yml --ref olm-vX.Y.Z \
  -f version=X.Y.Z \
  -f push_artifacts=true \
  -f force_release=true

gh workflow run .github/workflows/release-helm.yml --ref helm-vX.Y.Z \
  -f version=X.Y.Z \
  -f push_artifacts=true \
  -f force_release=true
```

Use the tag-triggered flow instead when backfill can tolerate repushing or creating the tag. Avoid hand-created releases unless the original artifact can no longer be reproduced; if manual backfill is necessary, include the exact registry references and attach regenerated manifests/chart packages from the tagged source.

For artifacts published before these workflow steps existed, first confirm the source commit that produced the artifact. If no release tag exists, create the component tag on that exact commit before creating a GitHub Release. Do not create a backfill release from the current branch unless it is known to match the published artifact.

## Repository Protection Baseline

Before first public release, enforce branch protection on `main`:

- Require pull request before merge
- Require approvals as needed for your team size:
  - solo maintainer default: `0` required approvals
  - multi-maintainer recommended: `1+` required approvals
- Require status checks to pass:
  - `rezzell/review-policy: main`
  - `rezzell/merge-readiness`

`rezzell/review-policy: main` records the configured review-policy approval gate.
`rezzell/merge-readiness` is the always-present GitHub Actions readiness check that
aggregates repository validation such as lint, tests, e2e, security, SonarCloud,
and workflow lint.
- Restrict direct pushes to `main`

Repository automation:

```bash
make branch-protect
```

To require explicit approvals once additional approvers exist:

```bash
REQUIRED_APPROVING_REVIEW_COUNT=1 make branch-protect
```

Notes:
- Requires GitHub CLI auth with admin permissions to the repository.
- The target branch (`main`) must already exist on the remote.
- Configure SonarCloud first (`SONAR_TOKEN`, disabled project Automatic Analysis, and optionally `SONAR_ORGANIZATION` / `SONAR_PROJECT_KEY`) before enforcing SonarCloud as a required check.

## Workflow Supply-Chain Policy

All GitHub Actions in this repository are pinned to immutable commit SHAs.
When updating an action:

1. Resolve the exact commit SHA for the desired upstream tag.
2. Replace `uses:` with the SHA and keep the source tag in an inline comment.
3. Validate with `Workflow Lint` and test CI before merging.

## API Maturity and Support Policy

- API version: `certificates.rezzell.com/v1alpha1`
- Maturity: `alpha` (breaking changes are allowed between minor versions)
- Compatibility goal:
  - No in-place CRD schema breaking change inside a patch release.
  - Breaking API/spec changes require a minor release note and migration guidance.
- Upgrade expectation:
  - Existing `CertificateJob` specs should continue to reconcile across patch upgrades.
  - For breaking alpha changes, provide conversion or a documented manifest migration path.

## Artifacts

- Operator workflow publishes/signs container image and records the immutable image digest in the GitHub Release.
- OLM workflow validates, publishes/signs bundle and catalog images, uploads generated `bundle/` + `catalog/` as workflow artifacts, and attaches the same generated content archive to the GitHub Release.
- Helm workflow lints/packages chart, uploads `.tgz` artifact, can push/sign OCI chart, and attaches the packaged chart to the GitHub Release.
