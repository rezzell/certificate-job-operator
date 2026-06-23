#!/usr/bin/env bash
set -euo pipefail

event_name="${GITHUB_EVENT_NAME:?GITHUB_EVENT_NAME is required}"
event_path="${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH is required}"
token="${GH_TOKEN:-}"
organization="${ORGANIZATION:?ORGANIZATION is required}"
scale_set_name="${SCALE_SET_NAME:?SCALE_SET_NAME is required}"
fallback_runner="${FALLBACK_RUNNER:?FALLBACK_RUNNER is required}"
public_runner_set_alias="preferred-runner-set"

log() {
  printf '%s\n' "$*" >&2
}

emit_runner() {
  local runner="$1"
  echo "runner=[\"${runner}\"]"
}

is_untrusted_fork_pr() {
  [[ "${event_name}" == "pull_request" ]] || return 1
  jq -e '.pull_request.head.repo.fork == true' "${event_path}" >/dev/null 2>&1
}

trusted_event() {
  case "${event_name}" in
    push|workflow_dispatch|schedule)
      return 0
      ;;
    pull_request)
      if is_untrusted_fork_pr; then
        return 1
      fi
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

fetch_mock_page() {
  local page="$1"
  local var_name="MOCK_RUNNERS_RESPONSE_PAGE_${page}"
  if [[ -n "${!var_name:-}" ]]; then
    printf '%s\n' "${!var_name}"
    return 0
  fi

  if [[ "${page}" == "1" && -n "${MOCK_RUNNERS_RESPONSE:-}" ]]; then
    printf '%s\n' "${MOCK_RUNNERS_RESPONSE}"
    return 0
  fi

  return 1
}

fetch_all_runners() {
  local online_count=0

  if [[ -n "${MOCK_RUNNERS_RESPONSE:-}" || -n "${MOCK_RUNNERS_RESPONSE_PAGE_1:-}" ]]; then
    local page=1
    local body
    while body="$(fetch_mock_page "${page}")"; do
      local page_count
      page_count="$(jq -r --arg prefix "${scale_set_name}" '[.runners[] | select(.status == "online" and (.name | startswith($prefix)))] | length' <<<"${body}")"
      online_count="$((online_count + page_count))"
      page="$((page + 1))"
    done
    printf '%s\n' "${online_count}"
    return 0
  fi

  local url="https://api.github.com/orgs/${organization}/actions/runners?per_page=100"
  while [[ -n "${url}" ]]; do
    local headers_file
    local body_file
    headers_file="$(mktemp)"
    body_file="$(mktemp)"

    curl -fsSL \
      -H "Authorization: Bearer ${token}" \
      -H "Accept: application/vnd.github+json" \
      -D "${headers_file}" \
      "${url}" \
      -o "${body_file}"

    local page_count
    page_count="$(jq -r --arg prefix "${scale_set_name}" '[.runners[] | select(.status == "online" and (.name | startswith($prefix)))] | length' "${body_file}")"
    online_count="$((online_count + page_count))"

    url="$(grep -i '^link:' "${headers_file}" | sed -n 's/.*<\([^>]*\)>; rel="next".*/\1/p' || true)"

    rm -f "${headers_file}" "${body_file}"
  done

  printf '%s\n' "${online_count}"
}

if is_untrusted_fork_pr; then
  log "Choose Runner: untrusted fork PR; using fallback runner ${fallback_runner}"
  emit_runner "${fallback_runner}"
  exit 0
fi

if ! trusted_event; then
  log "Choose Runner: unsupported or untrusted event ${event_name}; using fallback runner ${fallback_runner}"
  emit_runner "${fallback_runner}"
  exit 0
fi

if [[ -z "${token}" ]]; then
  log "Choose Runner: trusted event but no org-runners-read-token secret available; using fallback runner ${fallback_runner}"
  emit_runner "${fallback_runner}"
  exit 0
fi

online_count="$(fetch_all_runners)"

if [[ "${online_count}" -gt 0 ]]; then
  log "Choose Runner: found ${online_count} online runner(s) for the preferred runner set; using ${public_runner_set_alias}"
  emit_runner "${scale_set_name}"
else
  log "Choose Runner: found no online runners for the preferred runner set; using fallback runner ${fallback_runner}"
  emit_runner "${fallback_runner}"
fi
