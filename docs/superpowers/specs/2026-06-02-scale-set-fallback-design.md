# Scale-Set Aware Runner Fallback Design

## Context

The current `lint.yml` workflow in this repository uses a job-local runner selection step to decide between the ARC scale set and `ubuntu-latest`. That approach worked well enough to prove out the fallback idea, but it is not a good long-term boundary:

- it is tied to one job in one workflow
- it duplicates policy that should be reusable across repositories
- it depends on runner metadata that is not consistently exposed in the shape expected by label-based fallback actions

The desired end state is a reusable org-level unit that decides whether the GitHub-visible ARC scale set is available. If the scale set does not appear online, the workflow should fall back to GitHub-hosted infrastructure.

This design deliberately stays within GitHub-visible runner state only. It does not depend on Kubernetes health endpoints, custom controller probes, or any external service.

## Goals

1. Provide a reusable runner-selection unit that can be called from multiple workflows and repositories.
2. Prefer the ARC scale set when GitHub-visible runner state shows it is online.
3. Fall back to `ubuntu-latest` when the scale set does not appear online.
4. Keep the consumer workflow small and stable.
5. Make the implementation swappable later without changing every consumer.

## Non-Goals

1. Do not build a custom Kubernetes health service.
2. Do not encode ARC-specific logic into each consumer workflow.
3. Do not depend on per-runner custom labels as the primary routing signal.
4. Do not change the repository's security posture by weakening existing checks.

## Proposed Shape

The reusable unit should live in the organization `.github` repository as a reusable workflow. That keeps the policy centralized and lets repository workflows call it with a single `uses:` reference.

The workflow takes the following inputs:

- organization name
- runner scale set name
- fallback GitHub-hosted runner label
- GitHub token secret reference

The workflow returns one output:

- `runner`: a JSON array suitable for `fromJson(...)` in `runs-on`

## Decision Rule

Because the GitHub runner API currently exposes individual runner instances rather than a direct "scale set online" field, the reusable workflow will infer scale-set availability from GitHub-visible runner state:

1. Query the organization runners API.
2. Filter to runners whose `status` is `online`.
3. Treat runners whose names match the scale set name prefix as belonging to the scale set.
4. If at least one matching runner is online, return the scale-set target.
5. Otherwise return the GitHub-hosted fallback target.

This is an inference, not a direct scale-set health API. If GitHub later exposes a better scale-set endpoint, the reusable workflow can be updated centrally without changing consumers.

## Consumer Usage

Consumer workflows will call the reusable workflow and wire its output into `runs-on`.

Example shape:

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
    needs: choose-runner
    runs-on: ${{ fromJson(needs.choose-runner.outputs.runner) }}
    container:
      image: golang:1.25.10@sha256:c138bff780910acf4254ab3a6f7ff0f64bbd841f27bd82bfa986fe122c109538
    steps:
      - uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5 # v4
      - uses: actions/cache@0057852bfaa89a56745cba8c7296529d2fc39830 # v4
        with:
          path: |
            /tmp/certjob-go-build-cache
            /tmp/certjob-go-mod-cache
          key: ${{ runner.os }}-go-lint-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            ${{ runner.os }}-go-lint-
      - run: make lint
```

## Error Handling

If the runner query fails, the reusable workflow should fail closed by default. That keeps the signal explicit instead of silently routing around a bad API response.

The steady-state rule is simpler:

- if GitHub runner visibility can be read and a matching online scale-set runner exists, use it
- if GitHub runner visibility can be read and no matching online scale-set runner exists, fall back to the GitHub-hosted runner

Any future "allow fallback on query failure" mode should be an explicit input, not an implicit behavior change.

## Migration Plan

1. Keep the current repository-local experiment only long enough to validate the decision rule.
2. Move the selection logic into the organization `.github` reusable workflow.
3. Replace the repo-local runner selection step with a single call to the reusable workflow.
4. Leave job execution details such as container image and caching in the consumer repository.

## Testing And Verification

The reusable workflow should be validated against:

1. A runner API response that contains at least one online runner for the scale set.
2. A runner API response that contains only offline or unrelated runners.
3. A runner API response that is paginated.
4. A runner API failure case that confirms the fallback policy is intentional.

The consumer repository should verify that the returned JSON output is valid for `runs-on` and that the lint job still uses the pinned Go container and cache.

## Open Questions

1. Whether the reusable workflow should keep a future `fail-on-query-error` input.
2. Whether the scale-set matching rule should remain a name prefix or be tightened if GitHub exposes a better identifier.
3. Whether the same reusable workflow should support additional fallback targets beyond `ubuntu-latest`.
