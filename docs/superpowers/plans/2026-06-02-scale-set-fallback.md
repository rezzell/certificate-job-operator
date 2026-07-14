# Scale-Set Aware Runner Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move runner selection into a reusable org-level workflow that prefers the ARC scale set when GitHub-visible runner state shows it is online, and otherwise falls back to GitHub-hosted runners.

**Architecture:** Put the selection policy in the organization `.github` repository as a reusable workflow, not in a job-local helper. Consumer repositories call that reusable workflow to get a single `runs-on` JSON output, then keep job-specific concerns like container image and cache configuration locally. The reusable workflow uses only GitHub-visible runner state, so the decision is centralized and easy to swap later without editing every consumer.

**Tech Stack:** GitHub Actions reusable workflows, `curl`, `jq`, `bash`, `actions/checkout`, `actions/cache`, GitHub REST API, ARC/runner scale-set naming.

---

### Task 1: Create the org-level reusable runner-selection workflow

**Files:**
- Create: `.github/workflows/choose-runner.yml` in the organization `.github` repository

- [ ] **Step 1: Write the reusable workflow**

Use a `workflow_call` reusable workflow that accepts:
- `organization`
- `scale-set-name`
- `fallback-runner`
- `org-runners-read-token` secret

The workflow should:
- query `https://api.github.com/orgs/${organization}/actions/runners?per_page=100`
- follow pagination until no `rel="next"` link remains
- treat runners as usable when `.status == "online"`
- match the ARC scale set by checking that `.name` starts with the configured scale-set name
- output `runner` as JSON suitable for `fromJson(...)`

Use this shape as the starting point:

```yaml
name: Choose Runner

on:
  workflow_call:
    inputs:
      organization:
        required: true
        type: string
      scale-set-name:
        required: true
        type: string
      fallback-runner:
        required: true
        type: string
    secrets:
      org-runners-read-token:
        required: true
    outputs:
      runner:
        description: JSON array for runs-on
        value: ${{ jobs.choose.outputs.runner }}

jobs:
  choose:
    runs-on: ubuntu-latest
    outputs:
      runner: ${{ steps.select.outputs.runner }}
    steps:
      - name: Select runner target
        id: select
        env:
          GH_TOKEN: ${{ secrets.org-runners-read-token }}
          ORGANIZATION: ${{ inputs.organization }}
          SCALE_SET_NAME: ${{ inputs.scale-set-name }}
          FALLBACK_RUNNER: ${{ inputs.fallback-runner }}
        run: |
          set -euo pipefail
          online_count=0
          url="https://api.github.com/orgs/${ORGANIZATION}/actions/runners?per_page=100"

          while [[ -n "${url}" ]]; do
            headers_file="$(mktemp)"
            body_file="$(mktemp)"
            trap 'rm -f "${headers_file}" "${body_file}"' EXIT

            curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL \
              -H "Authorization: Bearer ${GH_TOKEN}" \
              -H "Accept: application/vnd.github+json" \
              -D "${headers_file}" \
              "${url}" \
              -o "${body_file}"

            page_count="$(jq -r --arg prefix "${SCALE_SET_NAME}" '[.runners[] | select(.status == "online" and (.name | startswith($prefix)))] | length' "${body_file}")"
            online_count="$((online_count + page_count))"

            url="$(grep -i '^link:' "${headers_file}" | sed -n 's/.*<\([^>]*\)>; rel="next".*/\1/p' || true)"

            rm -f "${headers_file}" "${body_file}"
            trap - EXIT
          done

          if [[ "${online_count}" -gt 0 ]]; then
            echo "runner=[\"${SCALE_SET_NAME}\"]" >> "${GITHUB_OUTPUT}"
          else
            echo "runner=[\"${FALLBACK_RUNNER}\"]" >> "${GITHUB_OUTPUT}"
          fi
```

- [ ] **Step 2: Validate the workflow syntax**

Run:

```bash
actionlint .github/workflows/choose-runner.yml
```

Expected:
- no YAML or GitHub Actions syntax errors
- pagination is implemented explicitly instead of assuming page 1 is enough

- [ ] **Step 3: Commit the reusable workflow**

```bash
git add .github/workflows/choose-runner.yml
git commit -m "ci: add reusable runner selection workflow"
```

### Task 2: Switch this repository to the reusable workflow

**Files:**
- Modify: `.github/workflows/lint.yml`

- [ ] **Step 1: Replace the local selector with a reusable workflow call**

Remove the temporary debug step and the repo-local runner-selection step. Add a `choose-runner` job that calls the org reusable workflow and passes the org token secret through.

Use this structure:

```yaml
jobs:
  runner-config:
    runs-on: ubuntu-latest
    outputs:
      scale-set-name: ${{ steps.config.outputs.scale-set-name }}
    steps:
      - id: config
        env:
          RUNNER_SCALE_SET_NAME: ${{ vars.RUNNER_SCALE_SET_NAME }}
          FALLBACK_RUNNER: ubuntu-latest
        run: echo "scale-set-name=${RUNNER_SCALE_SET_NAME:-${FALLBACK_RUNNER}}" >> "${GITHUB_OUTPUT}"

  choose-runner:
    needs: runner-config
    uses: rezzell/.github/.github/workflows/choose-runner.yml@main
    with:
      organization: rezzell
      scale-set-name: ${{ needs.runner-config.outputs.scale-set-name }}
      fallback-runner: ubuntu-latest
    secrets:
      org-runners-read-token: ${{ secrets.ORG_RUNNERS_READ_TOKEN }}

  lint:
    name: Lint (Ubuntu)
    needs: choose-runner
    runs-on: ${{ fromJson(needs.choose-runner.outputs.runner) }}
    container:
      image: golang:1.25.10@sha256:c138bff780910acf4254ab3a6f7ff0f64bbd841f27bd82bfa986fe122c109538
    env:
      GOCACHE: /tmp/certjob-go-build-cache
      GOMODCACHE: /tmp/certjob-go-mod-cache
    steps:
      - name: Clone the code
        uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5 # v4

      - name: Cache Go build and module directories
        uses: actions/cache@0057852bfaa89a56745cba8c7296529d2fc39830 # v4
        with:
          path: |
            /tmp/certjob-go-build-cache
            /tmp/certjob-go-mod-cache
          key: ${{ runner.os }}-go-lint-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            ${{ runner.os }}-go-lint-

      - name: Run linter
        run: make lint
```

- [ ] **Step 2: Keep the job container and cache settings intact**

Do not reintroduce `actions/setup-go`. The container already supplies the toolchain, and `actions/cache` should preserve the module/build caches across runs.

- [ ] **Step 3: Validate the workflow syntax locally**

Run:

```bash
actionlint .github/workflows/lint.yml
```

Expected:
- no syntax or schema errors

- [ ] **Step 4: Commit the consumer workflow update**

```bash
git add .github/workflows/lint.yml
git commit -m "ci: use reusable runner selection workflow"
```

### Task 3: Verify the reusable workflow and consumer behavior end-to-end

**Files:**
- No new files expected; this is verification only

- [ ] **Step 1: Push the reusable workflow to the org `.github` repository**

Push the org-level workflow change first so the consumer repo can reference it.

- [ ] **Step 2: Push this repository branch**

Push the consumer repository branch containing the updated `lint.yml`.

- [ ] **Step 3: Run the lint workflow on a pull request and inspect the runner-selection output**

Expected behavior:
- when `RUNNER_SCALE_SET_NAME` is configured and that scale set has at least one online runner visible to GitHub, the `choose-runner` job outputs the configured runner label
- when no matching online runner is visible, the `choose-runner` job outputs `["ubuntu-latest"]`
- the `lint` job still runs inside the pinned Go container and uses the cache paths

- [ ] **Step 4: Confirm the temporary debug logic is gone**

Verify that `lint.yml` no longer contains the ad hoc debug step or the previous repo-local runner-selection script.

- [ ] **Step 5: Commit any follow-up fixes**

If verification reveals a mismatch between the reusable workflow output and `runs-on`, fix the reusable workflow first, then update the consumer workflow if needed.
