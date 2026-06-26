#!/usr/bin/env bash
# Clean-machine smoke for the public Groundskeeper + OMP + Espalier install.
#
# Intended use on a disposable macOS/Linux VM:
#   export OLLAMA_CLOUD_API_KEY='<your ollama cloud key>'
#   curl -fsSL https://raw.githubusercontent.com/potato-hash/groundskeeper/main/scripts/smoke-public-install.sh | bash
#
# The script captures installer and verifier output, checks each for sensitive
# environment values before printing it, then reports the checked state scan.

set -euo pipefail

REPO="${GK_SMOKE_REPO:-potato-hash/groundskeeper}"
REF="${GK_SMOKE_REF:-main}"
MODEL="${GK_SMOKE_MODEL:-ollama-cloud/glm-5.2}"
RAW_BASE="https://raw.githubusercontent.com/${REPO}/${REF}"
API_BASE="https://api.github.com/repos/${REPO}/contents"
if [[ "${GK_SMOKE_USE_API_RAW:-0}" == "1" ]]; then
  DEFAULT_INSTALL_URL="${API_BASE}/install.sh?ref=${REF}"
  DEFAULT_VERIFY_URL="${API_BASE}/scripts/verify-install-state.sh?ref=${REF}"
else
  DEFAULT_INSTALL_URL="${RAW_BASE}/install.sh"
  DEFAULT_VERIFY_URL="${RAW_BASE}/scripts/verify-install-state.sh"
fi
INSTALL_URL="${GK_SMOKE_INSTALL_URL:-$DEFAULT_INSTALL_URL}"
VERIFY_URL="${GK_SMOKE_VERIFY_URL:-$DEFAULT_VERIFY_URL}"
VERIFY_MODEL="${GK_SMOKE_VERIFY_MODEL:-1}"
INSTALL_DIR="${GK_SMOKE_INSTALL_DIR:-}"
# Keep clean-machine smokes from writing OMP/Bun globals outside the smoke HOME.
export BUN_INSTALL="${GK_SMOKE_BUN_INSTALL:-$HOME/.bun}"
# Keep smoke runs deterministic even from a dev checkout with ../espalier nearby.
if [[ -z "${GK_ESPALIER_PATH:-}" ]]; then
  data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
  export GK_ESPALIER_PATH="${data_home}/groundskeeper/espalier"
fi

ok() { printf '[OK]   %s\n' "$*"; }
fail() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

is_sensitive_env_name() {
  local upper
  upper="$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')"
  [[ "$upper" == *API_KEY* ||
     "$upper" == *TOKEN* ||
     "$upper" == *SECRET* ||
     "$upper" == *PASSWORD* ||
     "$upper" == *PRIVATE_KEY* ||
     "$upper" == *ACCESS_KEY* ]]
}

scan_output_for_secret_values() {
  local log_file="$1"
  local output_label="${2:-install output}"
  local found=0
  local name value

  while IFS='=' read -r name value; do
    [[ -n "${name:-}" && ${#value} -ge 4 ]] || continue
    is_sensitive_env_name "$name" || continue
    if grep -Fq -- "$value" "$log_file"; then
      printf '[FAIL] sensitive value from %s appeared in %s\n' "$name" "$output_label" >&2
      found=1
    fi
  done < <(env)

  [[ "$found" -eq 0 ]]
}

has_sensitive_env_values() {
  local name value

  while IFS='=' read -r name value; do
    [[ -n "${name:-}" && ${#value} -ge 4 ]] || continue
    is_sensitive_env_name "$name" || continue
    return 0
  done < <(env)

  return 1
}

verifier_reports_secret_scan() {
  local log_file="$1"
  if has_sensitive_env_values; then
    grep -Fq '[OK] secret persistence scan passed across ' "$log_file"
  else
    grep -Fq '[OK] secret persistence scan passed across ' "$log_file" ||
      grep -Fq '[WARN] no sensitive env values available to scan' "$log_file"
  fi
}

has_secret_material() {
  has_sensitive_env_values && return 0
  [[ -f "$HOME/.omp/agent/agent.db" ]]
}

fetch_script() {
  local url="$1"
  case "$url" in
    https://api.github.com/repos/*/contents/*)
      curl -fsSL -H 'Accept: application/vnd.github.raw' "$url"
      ;;
    *)
      curl -fsSL "$url"
      ;;
  esac
}

is_trusted_smoke_source() {
  [[ "$REPO" == "potato-hash/groundskeeper" ]] || return 1
  [[ "$INSTALL_URL" == "$DEFAULT_INSTALL_URL" ]] || return 1
  [[ "$VERIFY_URL" == "$DEFAULT_VERIFY_URL" ]] || return 1
  if [[ "$REF" == "main" ]]; then
    return 0
  fi
  [[ "${GITHUB_ACTIONS:-}" == "true" &&
     "${GITHUB_REF_NAME:-}" == "main" &&
     "${GITHUB_REPOSITORY:-}" == "$REPO" &&
     -n "${GITHUB_SHA:-}" &&
     "$REF" == "$GITHUB_SHA" ]]
}

source_needs_trust() {
  has_secret_material && return 0
  [[ "$VERIFY_MODEL" != "0" && "$MODEL" == ollama-cloud/* ]]
}

if source_needs_trust && [[ "${GK_SMOKE_ALLOW_UNTRUSTED:-0}" != "1" ]] && ! is_trusted_smoke_source; then
  fail "secret-bearing public smoke only runs trusted Groundskeeper scripts; set GK_SMOKE_VERIFY_MODEL=0 without secrets for local stubs or GK_SMOKE_ALLOW_UNTRUSTED=1 to override"
fi

if [[ "$VERIFY_MODEL" != "0" ]]; then
  case "$MODEL" in
    ollama-cloud/*)
      if [[ -z "${OLLAMA_CLOUD_API_KEY:-${OLLAMA_API_KEY:-}}" && -z "${OMP_AUTH_BROKER_TOKEN:-}" && ! -f "$HOME/.omp/agent/agent.db" ]]; then
        fail "set OLLAMA_CLOUD_API_KEY or OMP_AUTH_BROKER_TOKEN, log in with OMP, or set GK_SMOKE_VERIFY_MODEL=0 to skip model verification"
      fi
      ;;
  esac
fi

log_file="$(mktemp "${TMPDIR:-/tmp}/groundskeeper-install-smoke.XXXXXX")"
verify_log_file="$(mktemp "${TMPDIR:-/tmp}/groundskeeper-verify-smoke.XXXXXX")"
trap 'rm -f "$log_file" "$verify_log_file"' EXIT

install_args=(--non-interactive --run-setup --model "$MODEL")
if [[ -n "$INSTALL_DIR" ]]; then
  install_args+=(--dir "$INSTALL_DIR")
  export PATH="$INSTALL_DIR:$PATH"
fi
if [[ "$VERIFY_MODEL" != "0" ]]; then
  install_args+=(--verify-model)
fi

printf '[INFO] Installing Groundskeeper from %s\n' "$INSTALL_URL"
set +e
{ fetch_script "$INSTALL_URL" | bash -s -- "${install_args[@]}"; } >"$log_file" 2>&1
install_status=$?
set -e

if ! scan_output_for_secret_values "$log_file"; then
  rm -f "$log_file" "$verify_log_file"
  exit 1
fi

cat "$log_file"
if [[ "$install_status" -ne 0 ]]; then
  fail "public install command failed with exit ${install_status}"
fi
if [[ "$VERIFY_MODEL" != "0" ]] && ! grep -Fq '[OK] OMP model smoke test passed' "$log_file"; then
  fail "model verification was requested, but setup did not report a passed OMP smoke test"
fi
ok "installer output did not contain sensitive environment values"

printf '[INFO] Verifying install state from %s\n' "$VERIFY_URL"
set +e
{ fetch_script "$VERIFY_URL" | bash; } >"$verify_log_file" 2>&1
verify_status=$?
set -e

if ! scan_output_for_secret_values "$verify_log_file" "verifier output"; then
  rm -f "$log_file" "$verify_log_file"
  exit 1
fi

cat "$verify_log_file"
if [[ "$verify_status" -ne 0 ]]; then
  fail "install-state verifier failed with exit ${verify_status}"
fi
if ! verifier_reports_secret_scan "$verify_log_file"; then
  fail "install-state verifier did not report secret persistence scan evidence"
fi
ok "verifier output did not contain sensitive environment values"
ok "install-state verifier reported secret persistence scan evidence"
ok "public install smoke completed"
