#!/usr/bin/env bash
# Clean-machine smoke for the public Groundskeeper + OMP + Espalier install.
#
# Intended use on a disposable macOS/Linux VM:
#   export OLLAMA_CLOUD_API_KEY='<your ollama cloud key>'
#   curl -fsSL https://raw.githubusercontent.com/potato-hash/groundskeeper/main/scripts/smoke-public-install.sh | bash
#
# The script captures installer output, checks it for sensitive environment
# values before printing it, then runs the checked-in post-install state scan.

set -euo pipefail

REPO="${GK_SMOKE_REPO:-potato-hash/groundskeeper}"
REF="${GK_SMOKE_REF:-main}"
MODEL="${GK_SMOKE_MODEL:-ollama-cloud/glm-5.2}"
INSTALL_URL="${GK_SMOKE_INSTALL_URL:-https://raw.githubusercontent.com/${REPO}/${REF}/install.sh}"
VERIFY_URL="${GK_SMOKE_VERIFY_URL:-https://raw.githubusercontent.com/${REPO}/${REF}/scripts/verify-install-state.sh}"
VERIFY_MODEL="${GK_SMOKE_VERIFY_MODEL:-1}"
INSTALL_DIR="${GK_SMOKE_INSTALL_DIR:-}"

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
  local found=0
  local name value

  while IFS='=' read -r name value; do
    [[ -n "${name:-}" && ${#value} -ge 4 ]] || continue
    is_sensitive_env_name "$name" || continue
    if grep -Fq -- "$value" "$log_file"; then
      printf '[FAIL] sensitive value from %s appeared in install output\n' "$name" >&2
      found=1
    fi
  done < <(env)

  [[ "$found" -eq 0 ]]
}

if [[ "$VERIFY_MODEL" != "0" ]]; then
  case "$MODEL" in
    ollama-cloud/*)
      if [[ -z "${OLLAMA_CLOUD_API_KEY:-${OLLAMA_API_KEY:-}}" ]]; then
        fail "set OLLAMA_CLOUD_API_KEY in the environment, or set GK_SMOKE_VERIFY_MODEL=0 to skip model verification"
      fi
      ;;
  esac
fi

log_file="$(mktemp "${TMPDIR:-/tmp}/groundskeeper-install-smoke.XXXXXX.log")"
trap 'rm -f "$log_file"' EXIT

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
bash -c 'curl -fsSL "$1" | bash -s -- "${@:2}"' bash "$INSTALL_URL" "${install_args[@]}" >"$log_file" 2>&1
install_status=$?
set -e

if ! scan_output_for_secret_values "$log_file"; then
  rm -f "$log_file"
  exit 1
fi

cat "$log_file"
if [[ "$install_status" -ne 0 ]]; then
  fail "public install command failed with exit ${install_status}"
fi
ok "installer output did not contain sensitive environment values"

printf '[INFO] Verifying install state from %s\n' "$VERIFY_URL"
curl -fsSL "$VERIFY_URL" | bash
ok "public install smoke completed"
