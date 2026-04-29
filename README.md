# certificate-job-operator

`certificate-job-operator` is a Kubernetes Operator (Operator SDK + controller-runtime) that watches cert-manager `Certificate` resources and runs one-or-more Kubernetes `Job`s whenever certificate inputs change.

The operator is designed for post-certificate workflows such as secret copy/sync, trust store update, notifications, or custom multi-step automation.

## What It Does

- Watches `cert-manager.io/v1` `Certificate` resources.
- Matches certificates to `CertificateJob` custom resources via label selectors.
- Computes an input hash from Certificate spec/status and referenced Secret data.
- Uses hash-based dedup so the same input is processed once.
- Executes 1..N Jobs as a DAG (`workflow.edges`) with configurable parallelism.
- Mounts certificate Secret data into each Job at `/var/run/certificate-input`.
- Records workflow state per Certificate in `.status.observedCertificates`.

## API Overview (`certificates.rezzell.com/v1alpha1`)

`CertificateJob` is namespace-scoped.

- `spec.certificateSelector`: label selector for Certificate objects.
- `spec.jobs[]`: named Job templates.
- `spec.workflow.edges[]`: DAG dependencies (`from` -> `to`).
- `spec.parallelism`: max concurrently runnable nodes (default `1`).
- `spec.jobTTLSecondsAfterFinished`: default TTL for created Jobs (default `3600`).
- `spec.failurePolicy`: `StopDownstream`, `ContinueIndependent`, `BestEffort`.

Security defaults:
- Certificate selection is restricted to the `CertificateJob` namespace.
- Job templates cannot use `hostPath`, `hostNetwork`, `hostPID`, `hostIPC`, root user, or privilege escalation.
- `serviceAccountName` override is rejected for job templates.
- Spawned jobs default `automountServiceAccountToken=false`, `enableServiceLinks=false`, `seccomp=RuntimeDefault`, and drop `ALL` capabilities.

See sample: `config/samples/certificates_v1alpha1_certificatejob.yaml`.

## Tenant RBAC and Broker Egress

Grant namespace owners permission to manage `CertificateJob` in a namespace:

```bash
kubectl apply -f config/samples/certificatejob_namespace_owner_rbac.yaml -n <tenant-namespace>
```

Restrict operator-managed workflow pods to broker-only egress in a namespace:

```bash
kubectl label ns <broker-namespace> certificate-job-broker=true
kubectl apply -f config/samples/networkpolicy_certificatejob_broker_egress.yaml -n <tenant-namespace>
```

This supports external delivery through a controlled broker endpoint instead of allowing arbitrary direct internet egress from workflow jobs.

## Local Development

```bash
make generate
make manifests
make test
make run
```

Optional namespace scoping at runtime:

```bash
WATCH_NAMESPACE=ns-a,ns-b make run
```

If `WATCH_NAMESPACE` is empty, all namespaces are watched.

Cluster smoke helper:

```bash
make smoke-e2e
```

## Build and Publish Images

```bash
make docker-build IMG=quay.io/<org>/certificate-job-operator:v0.1.0
make docker-push IMG=quay.io/<org>/certificate-job-operator:v0.1.0
```

## OLM Bundle

```bash
make bundle IMG=quay.io/<org>/certificate-job-operator:v0.1.0
make bundle-build BUNDLE_IMG=quay.io/<org>/certificate-job-operator-bundle:v0.1.0
make bundle-push BUNDLE_IMG=quay.io/<org>/certificate-job-operator-bundle:v0.1.0
```

Default bundle metadata:

- Version: `0.1.0`
- Channels: `alpha,stable`
- Default channel: `alpha`

## File-Based Catalog (Single Operator Catalog Source)

This repo includes an FBC layout in `catalog/` and a catalog image Dockerfile (`catalog.Dockerfile`).

```bash
make catalog BUNDLE_IMG=quay.io/<org>/certificate-job-operator-bundle:v0.1.0
make catalog-validate
make catalog-build CATALOG_IMG=quay.io/<org>/certificate-job-operator-catalog:v0.1.0
make catalog-push CATALOG_IMG=quay.io/<org>/certificate-job-operator-catalog:v0.1.0
```

## Helm Chart (Non-OLM Install)

This repo includes a Helm chart at `charts/certificate-job-operator`.

Secure defaults:
- namespace-scoped RBAC by default (`rbac.clusterScoped=false`)
- controller watch scope defaults to release namespace
- admission webhooks enabled with cert-manager-backed serving certs

Install:

```bash
helm upgrade --install certificate-job-operator ./charts/certificate-job-operator \
  --namespace certificate-job-operator-system \
  --create-namespace \
  --set image.repository=ghcr.io/<org>/certificate-job-operator \
  --set image.tag=v0.1.0
```

Optional cluster-scoped mode (cluster-owner only):

```bash
helm upgrade --install certificate-job-operator ./charts/certificate-job-operator \
  --namespace certificate-job-operator-system \
  --create-namespace \
  --set image.repository=ghcr.io/<org>/certificate-job-operator \
  --set image.tag=v0.1.0 \
  --set rbac.clusterScoped=true
```

Local chart validation/package:

```bash
make helm-lint
make helm-package VERSION=0.1.0
```

## Install via OLM

OLM deployment is namespace-scoped by default (`OperatorGroup.targetNamespaces` + CSV install modes for `OwnNamespace`/`SingleNamespace`).

1. Update image references in:
   - `config/olm/catalogsource.yaml`
   - `config/manager/manager.yaml` (or rely on bundle CSV image)
2. Apply manifests:

```bash
kubectl apply -f config/olm/catalogsource.yaml
kubectl apply -f config/olm/operatorgroup.yaml
kubectl apply -f config/olm/subscription.yaml
```

3. Verify installed CSV and operator pods:

```bash
kubectl get csv -n certificate-job-operator-system
kubectl get pods -n certificate-job-operator-system
```

## Security Automation

GitHub Actions includes `security.yml` with:
- dependency review on pull requests
- `govulncheck` on Go modules/code
- `gosec` static analysis
- immutable SHA-pinned workflow actions

GitHub Actions also includes `sonarcloud.yml` with:
- SonarCloud static analysis + quality gate enforcement
- Go coverage upload from `cover.out`
- immutable SHA-pinned SonarCloud action

SonarCloud setup:
- add repository secret `SONAR_TOKEN`
- optionally set repository variable `SONAR_ORGANIZATION` (defaults to `github.repository_owner`)
- optionally set repository variable `SONAR_PROJECT_KEY` (defaults to `<owner>_<repo>`)

## Split Release Pipelines

The project now has separate release workflows so operator, OLM artifacts, and Helm can be released independently.

- Operator image release: `.github/workflows/release-operator.yml`
  - Trigger tag: `operator-v<version>` (example: `operator-v0.2.0`)
- OLM bundle/catalog release: `.github/workflows/release-olm.yml`
  - Trigger tag: `olm-v<version>`
- Helm chart release (OCI): `.github/workflows/release-helm.yml`
  - Trigger tag: `helm-v<version>`

Each workflow also supports manual `workflow_dispatch` for explicit one-off releases.

Full release runbook (tag naming, path guards, overrides): `RELEASING.md`.
Branch protection can be applied via `make branch-protect` after the remote `main` branch exists.
Release dry-runs can be triggered with `make release-dry-run VERSION=<x.y.z>`.
