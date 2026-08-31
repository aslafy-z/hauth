#!/usr/bin/env bash
set -euo pipefail

# Workspace startup for the hauth HAPI image: HAPI (node + the hub package)
# is baked into the image, harness CLIs install natively with
# `hauth install`, then the hauth agent, HAPI hub, and HAPI runner start.
# Idempotent across workspace restarts.

AGENT_HARNESS="${AGENT_HARNESS:-hapi-only}"
HOME="${HOME:-/home/coder}"
PROJECT_DIR="${PROJECT_DIR:-${HOME}/project}"
HAPI_HOME="${HAPI_HOME:-${HOME}/.hapi}"
HAPI_NPM_PACKAGE="${HAPI_NPM_PACKAGE:-@twsxtd/hapi}"
HAPI_HOST="${HAPI_HOST:-127.0.0.1}"
HAPI_PORT="${HAPI_PORT:-3006}"
HAUTH_HOST="${HAUTH_HOST:-127.0.0.1}"
HAUTH_PORT="${HAUTH_PORT:-43117}"
HAUTH_BIN="${HAUTH_BIN:-/usr/local/bin/hauth}"
HAUTH_LISTEN="${HAUTH_LISTEN:-${HAUTH_HOST}:${HAUTH_PORT}}"
CLI_API_TOKEN="${CLI_API_TOKEN:-token}"

export HOME PROJECT_DIR HAPI_HOME CLI_API_TOKEN HAUTH_LISTEN

ensure_dirs() {
  mkdir -p \
    "${PROJECT_DIR}" \
    "${HAPI_HOME}" \
    "${HOME}/.local/bin"

  if [ -d "${PROJECT_DIR}" ] && [ "$(id -u)" -ne 0 ] && [ ! -w "${PROJECT_DIR}" ] && command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    sudo chown "$(id -u):$(id -g)" "${PROJECT_DIR}" || true
  fi
}

setup_path() {
  export PATH="${HOME}/.local/bin:${PATH}"
}

# HAPI is baked into the image at build time. If it is missing (custom
# image, upgraded package name), install it into the user prefix with npm.
ensure_hapi() {
  if command -v hapi >/dev/null 2>&1; then
    return 0
  fi

  if ! command -v npm >/dev/null 2>&1; then
    echo "hapi is not installed and npm is unavailable to install it." >&2
    echo "Bake ${HAPI_NPM_PACKAGE} or npm into the workspace image." >&2
    return 1
  fi

  echo "Installing ${HAPI_NPM_PACKAGE} into ${HOME}/.local"
  npm install -g --prefix "${HOME}/.local" "${HAPI_NPM_PACKAGE}"
}

selected_harnesses() {
  case "${AGENT_HARNESS}" in
    hapi-only)
      echo "gh"
      ;;
    code)
      echo "gh claude"
      ;;
    agy)
      echo "gh agy"
      ;;
    codex)
      echo "gh codex"
      ;;
    copilot)
      echo "gh copilot"
      ;;
    opencode)
      echo "gh opencode"
      ;;
    all)
      echo "gh claude agy codex copilot opencode"
      ;;
    *)
      echo "Unsupported AGENT_HARNESS: ${AGENT_HARNESS}" >&2
      exit 1
      ;;
  esac
}

install_harness_clis() {
  local harnesses
  harnesses="$(selected_harnesses)"

  echo "Installing harness CLIs: ${harnesses}"
  # shellcheck disable=SC2086
  if ! "${HAUTH_BIN}" install ${harnesses}; then
    echo "hauth install failed for: ${harnesses}" >&2
    return 1
  fi
}

verify_binary() {
  local bin="$1"

  if ! command -v "${bin}" >/dev/null 2>&1; then
    echo "Expected binary not found after install: ${bin}" >&2
    return 1
  fi

  echo "Found ${bin}: $(command -v "${bin}")"

  case "${bin}" in
    hapi | claude | agy | codex | copilot | opencode)
      "${bin}" --version || true
      ;;
  esac
}

verify_selected_binaries() {
  verify_binary hapi
  verify_binary gh

  case "${AGENT_HARNESS}" in
    hapi-only)
      ;;
    code)
      verify_binary claude
      ;;
    agy)
      verify_binary agy
      ;;
    codex)
      verify_binary codex
      ;;
    copilot)
      verify_binary copilot
      ;;
    opencode)
      verify_binary opencode
      ;;
    all)
      verify_binary claude
      verify_binary agy
      verify_binary codex
      verify_binary copilot
      verify_binary opencode
      ;;
    *)
      echo "Unsupported AGENT_HARNESS: ${AGENT_HARNESS}" >&2
      exit 1
      ;;
  esac
}

install_hapi_and_selected_harnesses() {
  ensure_dirs
  setup_path
  ensure_hapi
  install_harness_clis
  verify_selected_binaries
}

hapi_process_running() {
  local pattern="$1"

  pgrep -u "$(id -u)" -x hapi -a 2>/dev/null | awk -v pattern="${pattern}" '$0 ~ pattern { found = 1 } END { exit !found }'
}

hauth_agent_pids() {
  pgrep -u "$(id -u)" -f "${HAUTH_BIN} agent" || true
}

start_hauth_agent() {
  if curl -fsS "http://${HAUTH_HOST}:${HAUTH_PORT}/healthz" >/dev/null 2>&1; then
    echo "hauth agent already running and healthy"
    return 0
  fi

  local pids
  pids="$(hauth_agent_pids)"

  if [ -n "${pids}" ]; then
    echo "hauth agent process exists but health check failed; restarting"
    printf '%s\n' "${pids}" | xargs -r kill || true
    sleep 1

    pids="$(hauth_agent_pids)"
    if [ -n "${pids}" ]; then
      printf '%s\n' "${pids}" | xargs -r kill -9 || true
    fi
  fi

  if [ ! -x "${HAUTH_BIN}" ]; then
    echo "hauth binary is missing or not executable: ${HAUTH_BIN}" >&2
    return 1
  fi

  echo "Starting hauth agent on ${HAUTH_LISTEN}"
  # HAUTH_USER, HAUTH_INSTANCE, HAUTH_ALLOWED_ORIGINS, HAUTH_STORE and the
  # other HAUTH_* settings pass through from the environment when set.
  nohup "${HAUTH_BIN}" agent >"${HAPI_HOME}/hauth.log" 2>&1 &
  local hauth_pid="$!"

  for _ in $(seq 1 30); do
    if curl -fsS "http://${HAUTH_HOST}:${HAUTH_PORT}/healthz" >/dev/null 2>&1; then
      echo "hauth agent is ready"
      return 0
    fi

    if ! kill -0 "${hauth_pid}" >/dev/null 2>&1; then
      echo "hauth agent exited before becoming ready" >&2
      tail -n 100 "${HAPI_HOME}/hauth.log" >&2 || true
      return 1
    fi

    sleep 1
  done

  echo "hauth agent did not become ready" >&2
  tail -n 100 "${HAPI_HOME}/hauth.log" >&2 || true
  return 1
}

start_hapi_hub() {
  if hapi_process_running 'hapi hub([[:space:]]|$)'; then
    echo "HAPI hub already running"
    return 0
  fi

  echo "Starting HAPI hub on ${HAPI_HOST}:${HAPI_PORT}"
  echo "HAPI CLI_API_TOKEN: ${CLI_API_TOKEN}"

  nohup hapi hub --no-relay \
    >"${HAPI_HOME}/hub.log" \
    2>&1 &

  for _ in $(seq 1 60); do
    if curl -fsS "http://${HAPI_HOST}:${HAPI_PORT}" >/dev/null 2>&1; then
      echo "HAPI hub is ready"
      return 0
    fi
    sleep 1
  done

  echo "HAPI hub did not become ready" >&2
  tail -n 100 "${HAPI_HOME}/hub.log" >&2 || true
  return 1
}

runner_supervisor_running() {
  pgrep -u "$(id -u)" -f "hapi-runner-supervisor ${PROJECT_DIR}" >/dev/null 2>&1
}

start_hapi_runner() {
  if runner_supervisor_running; then
    echo "HAPI runner supervisor already running for ${PROJECT_DIR}"
    return 0
  fi

  if hapi_process_running "hapi runner start.*--workspace-root ${PROJECT_DIR}([[:space:]]|$)"; then
    echo "HAPI runner already running unsupervised for ${PROJECT_DIR}; leaving it"
    return 0
  fi

  echo "Starting HAPI runner for workspace root ${PROJECT_DIR}"

  # A crashed runner deregisters the machine from the hub and every
  # per-machine RPC fails until someone restarts it, so keep it under a
  # restart loop instead of a one-shot spawn.
  nohup bash -c '
    while true; do
      hapi runner start --workspace-root "$1"
      echo "hapi runner exited with status $?; restarting in 5s"
      sleep 5
    done
  ' hapi-runner-supervisor "${PROJECT_DIR}" \
    >"${HAPI_HOME}/runner.log" \
    2>&1 &
}

case "${AGENT_HARNESS}" in
  none)
    echo "AGENT_HARNESS=none; skipping HAPI and harness installation"
    exit 0
    ;;
  hapi-only | code | agy | codex | copilot | opencode | all)
    install_hapi_and_selected_harnesses
    start_hauth_agent
    start_hapi_hub
    start_hapi_runner
    ;;
  *)
    echo "Unsupported AGENT_HARNESS: ${AGENT_HARNESS}" >&2
    exit 1
    ;;
esac
