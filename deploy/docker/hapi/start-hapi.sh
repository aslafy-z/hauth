#!/usr/bin/env bash
set -euo pipefail

# Workspace startup for the hauth HAPI image: installs HAPI and the selected
# harness CLIs with mise, starts the hauth agent on loopback, then starts the
# HAPI hub and runner. Idempotent across workspace restarts.

AGENT_HARNESS="${AGENT_HARNESS:-hapi-only}"
HOME="${HOME:-/home/coder}"
PROJECT_DIR="${PROJECT_DIR:-${HOME}/project}"
HAPI_HOME="${HAPI_HOME:-${HOME}/.hapi}"
MISE_GLOBAL_CONFIG_DIR="${MISE_GLOBAL_CONFIG_DIR:-${HOME}/.config/mise}"
MISE_CONFIG_FILE="${MISE_CONFIG_FILE:-${MISE_GLOBAL_CONFIG_DIR}/config.toml}"
HAPI_HOST="${HAPI_HOST:-127.0.0.1}"
HAPI_PORT="${HAPI_PORT:-3006}"
AUTHD_HOST="${AUTHD_HOST:-127.0.0.1}"
AUTHD_PORT="${AUTHD_PORT:-43117}"
HAUTH_BIN="${HAUTH_BIN:-/usr/local/bin/hauth}"
HAUTH_LISTEN="${HAUTH_LISTEN:-${AUTHD_HOST}:${AUTHD_PORT}}"
CLI_API_TOKEN="${CLI_API_TOKEN:-token}"

export HOME PROJECT_DIR HAPI_HOME MISE_CONFIG_FILE CLI_API_TOKEN HAUTH_LISTEN

ensure_dirs() {
  mkdir -p \
    "${PROJECT_DIR}" \
    "${HAPI_HOME}" \
    "${HOME}/.local/bin" \
    "${MISE_GLOBAL_CONFIG_DIR}"

  if [ -d "${PROJECT_DIR}" ] && [ "$(id -u)" -ne 0 ] && [ ! -w "${PROJECT_DIR}" ] && command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    sudo chown "$(id -u):$(id -g)" "${PROJECT_DIR}" || true
  fi
}

ensure_mise() {
  if command -v mise >/dev/null 2>&1; then
    mise --version
    return 0
  fi

  echo "mise is not installed or not on PATH." >&2
  echo "Install mise in the workspace base image." >&2
  return 1
}

setup_path() {
  export PATH="${HOME}/.local/bin:${HOME}/.local/share/mise/shims:${PATH}"
}

write_mise_config() {
  case "${AGENT_HARNESS}" in
    hapi-only)
      cat >"${MISE_CONFIG_FILE}" <<'CONFIG'
[tools]
node = "22"
"npm:@twsxtd/hapi" = "latest"
gh = "latest"
CONFIG
      ;;
    code)
      cat >"${MISE_CONFIG_FILE}" <<'CONFIG'
[tools]
node = "22"
"npm:@twsxtd/hapi" = "latest"
gh = "latest"
claude-code = "latest"
CONFIG
      ;;
    agy)
      cat >"${MISE_CONFIG_FILE}" <<'CONFIG'
[tools]
node = "22"
"npm:@twsxtd/hapi" = "latest"
gh = "latest"
antigravity-cli = "latest"
CONFIG
      ;;
    codex)
      cat >"${MISE_CONFIG_FILE}" <<'CONFIG'
[tools]
node = "22"
"npm:@twsxtd/hapi" = "latest"
gh = "latest"
codex = "latest"
CONFIG
      ;;
    opencode)
      cat >"${MISE_CONFIG_FILE}" <<'CONFIG'
[tools]
node = "22"
"npm:@twsxtd/hapi" = "latest"
gh = "latest"
opencode = "latest"
CONFIG
      ;;
    all)
      cat >"${MISE_CONFIG_FILE}" <<'CONFIG'
[tools]
node = "22"
"npm:@twsxtd/hapi" = "latest"
gh = "latest"
claude-code = "latest"
antigravity-cli = "latest"
codex = "latest"
opencode = "latest"
CONFIG
      ;;
    *)
      echo "Unsupported AGENT_HARNESS: ${AGENT_HARNESS}" >&2
      exit 1
      ;;
  esac

  echo "Generated mise config at ${MISE_CONFIG_FILE}"
  cat "${MISE_CONFIG_FILE}"
}

mise_install_tools() {
  echo "Installing tools from global mise config ${MISE_CONFIG_FILE}"

  if ! mise install; then
    echo "mise install failed for AGENT_HARNESS=${AGENT_HARNESS}" >&2
    echo "Config used:" >&2
    cat "${MISE_CONFIG_FILE}" >&2
    return 1
  fi

  mise reshim || true
  eval "$(mise env -s bash)"
}

verify_binary() {
  local bin="$1"

  if ! command -v "${bin}" >/dev/null 2>&1; then
    echo "Expected binary not found after install: ${bin}" >&2
    return 1
  fi

  echo "Found ${bin}: $(command -v "${bin}")"

  case "${bin}" in
    hapi | claude | agy | codex | opencode)
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
    opencode)
      verify_binary opencode
      ;;
    all)
      verify_binary claude
      verify_binary agy
      verify_binary codex
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
  ensure_mise
  write_mise_config
  mise trust "${MISE_CONFIG_FILE}"
  mise_install_tools
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
  if curl -fsS "http://${AUTHD_HOST}:${AUTHD_PORT}/healthz" >/dev/null 2>&1; then
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
    if curl -fsS "http://${AUTHD_HOST}:${AUTHD_PORT}/healthz" >/dev/null 2>&1; then
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

start_hapi_runner() {
  if hapi_process_running "hapi runner start.*--workspace-root ${PROJECT_DIR}([[:space:]]|$)"; then
    echo "HAPI runner already running for ${PROJECT_DIR}"
    return 0
  fi

  echo "Starting HAPI runner for workspace root ${PROJECT_DIR}"

  nohup hapi runner start --workspace-root "${PROJECT_DIR}" \
    >"${HAPI_HOME}/runner.log" \
    2>&1 &
}

case "${AGENT_HARNESS}" in
  none)
    echo "AGENT_HARNESS=none; skipping HAPI and harness installation"
    exit 0
    ;;
  hapi-only | code | agy | codex | opencode | all)
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
